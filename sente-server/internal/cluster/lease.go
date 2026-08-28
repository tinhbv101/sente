// Package cluster decides which node owns a live game.
//
// Exactly one node holds a game at a time (docs/03 ADR-005). That node keeps the
// game in memory, owns its clock, and is the only writer. Ownership is a lease in
// Redis with a short TTL, so a node that dies simply stops renewing and another
// takes over.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// DefaultTTL is how long a lease survives without renewal. Long enough to ride
	// out a GC pause or a slow Redis round trip, short enough that a dead node's
	// games are reclaimed well inside the ten seconds NFR-S4 allows.
	DefaultTTL = 30 * time.Second
	// RenewInterval is how often the owner refreshes. A third of the TTL leaves
	// room for two failed attempts before ownership is actually lost.
	RenewInterval = 10 * time.Second
	// NodeTTL is how long a node counts as alive without a heartbeat.
	NodeTTL = 15 * time.Second
)

var ErrNotOwner = errors.New("cluster: this node does not hold the lease")

type Leases struct {
	client *redis.Client
	nodeID string
	ttl    time.Duration
}

func New(client *redis.Client, nodeID string) *Leases {
	return &Leases{client: client, nodeID: nodeID, ttl: DefaultTTL}
}

// WithTTL returns a copy using a different lease lifetime. Tests use short ones.
func (l *Leases) WithTTL(ttl time.Duration) *Leases {
	copied := *l
	copied.ttl = ttl
	return &copied
}

func (l *Leases) NodeID() string { return l.nodeID }

func leaseKey(gameID string) string     { return "game:" + gameID + ":owner" }
func nodeGamesKey(nodeID string) string { return "node:" + nodeID + ":games" }

const nodesKey = "nodes"

// Renew and Release compare the stored value before acting. Without that, a node
// that already lost its lease could extend or delete the new owner's -- the
// classic distributed-lock bug.
var (
	renewScript = redis.NewScript(`
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("PEXPIRE", KEYS[1], ARGV[2])
		end
		return 0`)

	releaseScript = redis.NewScript(`
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			redis.call("DEL", KEYS[1])
			redis.call("SREM", KEYS[2], ARGV[2])
			return 1
		end
		return 0`)

	// Used by the reaper: only clears a lease still pointing at the dead node.
	reclaimScript = redis.NewScript(`
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("DEL", KEYS[1])
		end
		return 0`)
)

// Acquire takes ownership if the game is unowned. It returns false, not an error,
// when another node already holds it: losing a race is normal, not a fault.
func (l *Leases) Acquire(ctx context.Context, gameID string) (bool, error) {
	ok, err := l.client.SetNX(ctx, leaseKey(gameID), l.nodeID, l.ttl).Result()
	if err != nil {
		return false, fmt.Errorf("cluster: acquiring lease: %w", err)
	}
	if !ok {
		return false, nil
	}
	// The reverse index is what lets the reaper find a dead node's games.
	if err := l.client.SAdd(ctx, nodeGamesKey(l.nodeID), gameID).Err(); err != nil {
		return false, fmt.Errorf("cluster: indexing lease: %w", err)
	}
	return true, nil
}

// Renew extends the lease. False means ownership was lost -- the caller must stop
// writing to the game immediately.
func (l *Leases) Renew(ctx context.Context, gameID string) (bool, error) {
	result, err := renewScript.Run(ctx, l.client,
		[]string{leaseKey(gameID)}, l.nodeID, l.ttl.Milliseconds()).Int64()
	if err != nil {
		return false, fmt.Errorf("cluster: renewing lease: %w", err)
	}
	return result == 1, nil
}

// Release hands a game back voluntarily, which is what a draining node does so the
// next owner does not have to wait out the TTL (docs/04 §5.2).
func (l *Leases) Release(ctx context.Context, gameID string) error {
	result, err := releaseScript.Run(ctx, l.client,
		[]string{leaseKey(gameID), nodeGamesKey(l.nodeID)}, l.nodeID, gameID).Int64()
	if err != nil {
		return fmt.Errorf("cluster: releasing lease: %w", err)
	}
	if result == 0 {
		return ErrNotOwner
	}
	return nil
}

// Owner reports which node holds a game, or "" if nobody does.
func (l *Leases) Owner(ctx context.Context, gameID string) (string, error) {
	owner, err := l.client.Get(ctx, leaseKey(gameID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cluster: reading lease: %w", err)
	}
	return owner, nil
}

// HeldGames lists the games this node believes it owns.
func (l *Leases) HeldGames(ctx context.Context) ([]string, error) {
	games, err := l.client.SMembers(ctx, nodeGamesKey(l.nodeID)).Result()
	if err != nil {
		return nil, fmt.Errorf("cluster: listing held games: %w", err)
	}
	return games, nil
}

// ── node liveness ───────────────────────────────────────────────────────────

// Heartbeat marks this node alive. A sorted set keyed by timestamp is used rather
// than one key per node, so the reaper can find dead nodes with one range query
// instead of scanning the keyspace.
func (l *Leases) Heartbeat(ctx context.Context, now time.Time) error {
	err := l.client.ZAdd(ctx, nodesKey, redis.Z{
		Score: float64(now.UnixMilli()), Member: l.nodeID,
	}).Err()
	if err != nil {
		return fmt.Errorf("cluster: heartbeat: %w", err)
	}
	return nil
}

// LiveNodes are those that have beaten within NodeTTL.
func (l *Leases) LiveNodes(ctx context.Context, now time.Time) ([]string, error) {
	cutoff := now.Add(-NodeTTL).UnixMilli()
	nodes, err := l.client.ZRangeByScore(ctx, nodesKey, &redis.ZRangeBy{
		Min: fmt.Sprintf("%d", cutoff), Max: "+inf",
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("cluster: listing live nodes: %w", err)
	}
	return nodes, nil
}

// DeadNodes are those that have stopped beating but are still registered.
func (l *Leases) DeadNodes(ctx context.Context, now time.Time) ([]string, error) {
	cutoff := now.Add(-NodeTTL).UnixMilli()
	nodes, err := l.client.ZRangeByScore(ctx, nodesKey, &redis.ZRangeBy{
		Min: "-inf", Max: fmt.Sprintf("(%d", cutoff),
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("cluster: listing dead nodes: %w", err)
	}
	return nodes, nil
}

// Deregister removes this node from the roster, which a draining node does last.
func (l *Leases) Deregister(ctx context.Context) error {
	if err := l.client.ZRem(ctx, nodesKey, l.nodeID).Err(); err != nil {
		return fmt.Errorf("cluster: deregistering: %w", err)
	}
	return nil
}

// Reap frees the games of every node that has stopped beating.
//
// Leases expire on their own, so this is an accelerator, not a correctness
// requirement: without it a game waits out the full TTL before anyone can take it
// (docs/04 §4.5). It is safe to run on several nodes at once because each lease is
// only cleared if it still names the dead node.
func (l *Leases) Reap(ctx context.Context, now time.Time) (freed int, err error) {
	dead, err := l.DeadNodes(ctx, now)
	if err != nil {
		return 0, err
	}
	for _, node := range dead {
		games, err := l.client.SMembers(ctx, nodeGamesKey(node)).Result()
		if err != nil {
			return freed, fmt.Errorf("cluster: listing games of %s: %w", node, err)
		}
		for _, gameID := range games {
			cleared, err := reclaimScript.Run(ctx, l.client, []string{leaseKey(gameID)}, node).Int64()
			if err != nil {
				return freed, fmt.Errorf("cluster: reclaiming %s: %w", gameID, err)
			}
			freed += int(cleared)
		}
		// Only forget the node once its games are free, so a crash mid-reap leaves
		// the work for the next pass instead of stranding the leases.
		if err := l.client.Del(ctx, nodeGamesKey(node)).Err(); err != nil {
			return freed, err
		}
		if err := l.client.ZRem(ctx, nodesKey, node).Err(); err != nil {
			return freed, err
		}
	}
	return freed, nil
}
