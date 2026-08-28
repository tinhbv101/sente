// Package hub routes a command to whichever node owns the game, and fans the
// resulting events back out to every node with someone watching.
//
// Two players are often connected to different nodes, but a game has exactly one
// owner (docs/03 ADR-005). The node holding a connection is a gateway: it either
// runs the game itself or forwards.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"sente.app/server/internal/cluster"
	"sente.app/server/internal/game"
	"sente.app/server/internal/node"
	"sente.app/server/internal/wire"
)

const (
	// ForwardTimeout bounds how long a gateway waits for the owner to answer. A
	// command that times out is not lost: the client retries with the same
	// idempotency key, and the owner refuses the duplicate.
	ForwardTimeout = 5 * time.Second
	// replyTTL stops abandoned reply keys accumulating.
	replyTTL = 30 * time.Second
	// commandStreamCap bounds the backlog of a node that has stopped consuming.
	commandStreamCap = 10000
)

var ErrForwardTimeout = errors.New("hub: the owning node did not answer")

type Config struct {
	Registry *node.Registry
	Leases   *cluster.Leases
	Redis    *redis.Client
	NodeID   string
}

type Hub struct {
	config Config

	mu          sync.Mutex
	subscribers map[string]map[int]chan game.Event
	nextSubID   int
	pubsubs     map[string]*redis.PubSub
}

func New(config Config) *Hub {
	return &Hub{
		config:      config,
		subscribers: map[string]map[int]chan game.Event{},
		pubsubs:     map[string]*redis.PubSub{},
	}
}

func commandStream(nodeID string) string { return "node:" + nodeID + ":cmd" }
func eventChannel(gameID string) string  { return "game:" + gameID }

// Broadcast is wired into the registry so every event a local actor produces is
// published, wherever the watchers happen to be connected.
func (h *Hub) Broadcast(gameID string, events []game.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, event := range events {
		encoded, err := wire.EncodeEvent(event)
		if err != nil {
			continue
		}
		// Local watchers go through Redis too. One delivery path is easier to
		// reason about than two, and the extra millisecond does not matter next to
		// the round trip a client is already waiting on.
		_ = h.config.Redis.Publish(ctx, eventChannel(gameID), encoded).Err()
	}
}

// Execute runs a command on the node that owns the game, forwarding if that is
// not this one.
func (h *Hub) Execute(ctx context.Context, gameID string, command game.Command) ([]game.Event, error) {
	if actor, ok := h.config.Registry.Get(gameID); ok {
		events, _, err := actor.Send(ctx, command)
		// A stopped actor means the game is no longer runnable here. Fall through
		// and resolve ownership again rather than handing the client an error it
		// can do nothing with.
		if !errors.Is(err, game.ErrActorStopped) {
			return events, err
		}
	}

	owner, err := h.config.Leases.Owner(ctx, gameID)
	if err != nil {
		return nil, err
	}
	if owner != "" && owner != h.config.NodeID {
		return h.forward(ctx, owner, gameID, command)
	}
	if owner == h.config.NodeID {
		// We hold the lease but are not running the game -- adopt it rather than
		// forwarding to ourselves and finding nobody home.
		actor, err := h.config.Registry.Adopt(ctx, gameID)
		if err != nil {
			return nil, err
		}
		events, _, err := actor.Send(ctx, command)
		return events, err
	}

	// Unowned: take it and run it here.
	actor, err := h.config.Registry.Acquire(ctx, gameID)
	if errors.Is(err, node.ErrHeldElsewhere) {
		// Someone won the race between the two calls above.
		owner, ownerErr := h.config.Leases.Owner(ctx, gameID)
		if ownerErr != nil {
			return nil, ownerErr
		}
		if owner == "" {
			return nil, node.ErrHeldElsewhere
		}
		return h.forward(ctx, owner, gameID, command)
	}
	if err != nil {
		return nil, err
	}
	events, _, err := actor.Send(ctx, command)
	return events, err
}

type forwardRequest struct {
	GameID  string          `json:"game_id"`
	Command json.RawMessage `json:"command"`
	ReplyTo string          `json:"reply_to"`
}

type forwardResponse struct {
	Events []json.RawMessage `json:"events,omitempty"`
	Error  string            `json:"error,omitempty"`
}

func (h *Hub) forward(ctx context.Context, owner, gameID string,
	command game.Command) ([]game.Event, error) {
	encoded, err := wire.EncodeCommand(command)
	if err != nil {
		return nil, err
	}
	replyKey := fmt.Sprintf("reply:%s:%d", h.config.NodeID, time.Now().UnixNano())
	payload, err := json.Marshal(forwardRequest{
		GameID: gameID, Command: encoded, ReplyTo: replyKey,
	})
	if err != nil {
		return nil, err
	}

	err = h.config.Redis.XAdd(ctx, &redis.XAddArgs{
		Stream: commandStream(owner),
		MaxLen: commandStreamCap,
		Approx: true,
		Values: map[string]any{"payload": payload},
	}).Err()
	if err != nil {
		return nil, fmt.Errorf("hub: forwarding to %s: %w", owner, err)
	}

	result, err := h.config.Redis.BLPop(ctx, ForwardTimeout, replyKey).Result()
	if errors.Is(err, redis.Nil) {
		return nil, ErrForwardTimeout
	}
	if err != nil {
		return nil, fmt.Errorf("hub: waiting for %s: %w", owner, err)
	}
	if len(result) != 2 {
		return nil, ErrForwardTimeout
	}

	var response forwardResponse
	if err := json.Unmarshal([]byte(result[1]), &response); err != nil {
		return nil, err
	}
	if response.Error != "" {
		// The owner's verdict is the real one; the gateway must not soften it.
		return nil, commandError(response.Error)
	}
	events := make([]game.Event, 0, len(response.Events))
	for _, raw := range response.Events {
		event, err := wire.DecodeEvent(raw)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

// commandError carries a rejection across the wire. The code is the wire code the
// client already understands (docs/06 §3.6).
type commandError string

func (e commandError) Error() string { return string(e) }

// Serve consumes commands forwarded to this node until the context is cancelled.
func (h *Hub) Serve(ctx context.Context) {
	stream := commandStream(h.config.NodeID)
	lastID := "$"
	for {
		if ctx.Err() != nil {
			return
		}
		result, err := h.config.Redis.XRead(ctx, &redis.XReadArgs{
			Streams: []string{stream, lastID},
			Count:   32,
			Block:   time.Second,
		}).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		for _, entry := range result {
			for _, message := range entry.Messages {
				lastID = message.ID
				h.handleForwarded(ctx, message)
			}
		}
	}
}

func (h *Hub) handleForwarded(ctx context.Context, message redis.XMessage) {
	raw, ok := message.Values["payload"].(string)
	if !ok {
		return
	}
	var request forwardRequest
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		return
	}
	response := forwardResponse{}

	command, err := wire.DecodeCommand(request.Command)
	if err != nil {
		response.Error = err.Error()
	} else if actor, ok := h.config.Registry.Get(request.GameID); !ok {
		// The game moved on between the forward and its arrival. Saying so lets
		// the gateway look up the new owner and try again.
		response.Error = node.ErrHeldElsewhere.Error()
	} else {
		events, _, sendErr := actor.Send(ctx, command)
		if errors.Is(sendErr, game.ErrActorStopped) {
			// Stopped between the forward and its arrival: same answer as never
			// having had it, so the gateway looks the owner up again.
			sendErr = node.ErrHeldElsewhere
		}
		if sendErr != nil {
			response.Error = sendErr.Error()
		}
		for _, event := range events {
			encoded, encodeErr := wire.EncodeEvent(event)
			if encodeErr != nil {
				continue
			}
			response.Events = append(response.Events, encoded)
		}
	}

	payload, err := json.Marshal(response)
	if err != nil {
		return
	}
	pipe := h.config.Redis.Pipeline()
	pipe.LPush(ctx, request.ReplyTo, payload)
	pipe.Expire(ctx, request.ReplyTo, replyTTL)
	_, _ = pipe.Exec(ctx)
}

// Subscribe delivers a game's events to a local caller, whichever node produced
// them. The returned function unsubscribes.
func (h *Hub) Subscribe(ctx context.Context, gameID string) (<-chan game.Event, func()) {
	events := make(chan game.Event, 64)

	h.mu.Lock()
	id := h.nextSubID
	h.nextSubID++
	if h.subscribers[gameID] == nil {
		h.subscribers[gameID] = map[int]chan game.Event{}
		pubsub := h.config.Redis.Subscribe(ctx, eventChannel(gameID))
		h.pubsubs[gameID] = pubsub
		go h.relay(gameID, pubsub)
	}
	h.subscribers[gameID][id] = events
	h.mu.Unlock()

	return events, func() { h.unsubscribe(gameID, id) }
}

func (h *Hub) relay(gameID string, pubsub *redis.PubSub) {
	for message := range pubsub.Channel() {
		event, err := wire.DecodeEvent([]byte(message.Payload))
		if err != nil {
			continue
		}
		h.mu.Lock()
		targets := make([]chan game.Event, 0, len(h.subscribers[gameID]))
		for _, channel := range h.subscribers[gameID] {
			targets = append(targets, channel)
		}
		h.mu.Unlock()

		for _, channel := range targets {
			select {
			case channel <- event:
			default:
				// A subscriber that cannot keep up is dropped rather than allowed
				// to stall the relay; it recovers by resyncing (docs/06 §3.12).
			}
		}
	}
}

func (h *Hub) unsubscribe(gameID string, id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	channels := h.subscribers[gameID]
	if channels == nil {
		return
	}
	if channel, ok := channels[id]; ok {
		close(channel)
		delete(channels, id)
	}
	if len(channels) == 0 {
		delete(h.subscribers, gameID)
		if pubsub := h.pubsubs[gameID]; pubsub != nil {
			_ = pubsub.Close()
			delete(h.pubsubs, gameID)
		}
	}
}

// Close tears down every subscription.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for gameID, channels := range h.subscribers {
		for id, channel := range channels {
			close(channel)
			delete(channels, id)
		}
		if pubsub := h.pubsubs[gameID]; pubsub != nil {
			_ = pubsub.Close()
		}
		delete(h.pubsubs, gameID)
		delete(h.subscribers, gameID)
	}
}
