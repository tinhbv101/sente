// Package notify turns game events into pushes (docs/03 ADR-013). It sits next
// to the hub's Broadcast: the hub tells connected clients, this tells the ones
// who are away. Delivery is asynchronous so a slow APNs never slows a move.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sente.app/server/internal/game"
	"sente.app/server/internal/push"
	"sente.app/server/internal/rules"
	"sente.app/server/internal/store"
)

type Sender interface {
	Send(ctx context.Context, deviceToken string, n push.Notification) error
}

type Directory interface {
	Participants(ctx context.Context, gameID string) (store.Participants, error)
}

type Devices interface {
	ForUser(ctx context.Context, userID, kind string) ([]store.Device, error)
	Unregister(ctx context.Context, token string) error
}

type Config struct {
	Games   Directory
	Devices Devices
	// A device registers with the environment its build talks to; each needs its
	// own APNs host. Either may be nil.
	Sandbox    Sender
	Production Sender
	Logger     *slog.Logger
	QueueSize  int
}

// Notification kinds double as the preference keys in devices.push_prefs.
const (
	KindTurn    = "turn"
	KindGameEnd = "game_end"
	KindInvite  = "invite"
	KindLowTime = "low_time"
	KindFriend  = "friend"
)

type job struct {
	gameID string
	events []game.Event
	direct *directJob
}

type directJob struct {
	userID string
	kind   string
	note   push.Notification
}

type Notifier struct {
	config Config
	queue  chan job
}

func New(config Config) *Notifier {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.QueueSize <= 0 {
		config.QueueSize = 512
	}
	return &Notifier{config: config, queue: make(chan job, config.QueueSize)}
}

// Enabled is false without any APNs client, in which case every method is a
// no-op -- including on a nil *Notifier, so callers need no guard.
func (n *Notifier) Enabled() bool {
	return n != nil && (n.config.Sandbox != nil || n.config.Production != nil)
}

func (n *Notifier) enqueue(j job) {
	select {
	case n.queue <- j:
	default:
		// Pushes are reminders, not the record of the game. Dropping one under
		// load beats stalling the actor that produced it.
		n.config.Logger.Warn("push queue full, dropping notification", "game_id", j.gameID)
	}
}

// Broadcast has the registry's Broadcast signature so it can be chained after
// the hub's.
func (n *Notifier) Broadcast(gameID string, events []game.Event) {
	if !n.Enabled() {
		return
	}
	n.enqueue(job{gameID: gameID, events: events})
}

// InvitationAccepted tells the person who made an invitation that someone took it.
func (n *Notifier) InvitationAccepted(creatorID, byName, gameID string) {
	if !n.Enabled() {
		return
	}
	n.enqueue(job{gameID: gameID, direct: &directJob{userID: creatorID, kind: KindInvite, note: push.Notification{
		Title: "Lời mời đã được nhận", Body: fmt.Sprintf("%s đã vào ván. Đến lượt bạn.", byName),
		TitleKey: "push.invite.title", BodyKey: "push.invite.body", Args: []string{byName},
		CollapseID: "game:" + gameID, ThreadID: gameID,
		Payload: map[string]any{"game_id": gameID, "kind": KindInvite},
	}}})
}

// LowTime warns the player to move that their correspondence clock is nearly out.
func (n *Notifier) LowTime(userID, gameID string, remaining time.Duration) {
	if !n.Enabled() {
		return
	}
	var bodyKey, viBody string
	var amount int
	if remaining >= time.Hour {
		amount = int((remaining + time.Hour - 1) / time.Hour)
		bodyKey = "push.lowtime.hours"
		viBody = fmt.Sprintf("Còn dưới %d giờ cho nước đi của bạn.", amount)
	} else {
		amount = int((remaining + time.Minute - 1) / time.Minute)
		bodyKey = "push.lowtime.minutes"
		viBody = fmt.Sprintf("Còn khoảng %d phút cho nước đi của bạn.", amount)
	}
	n.enqueue(job{gameID: gameID, direct: &directJob{userID: userID, kind: KindLowTime, note: push.Notification{
		Title: "Sắp hết giờ", Body: viBody,
		TitleKey: "push.lowtime.title", BodyKey: bodyKey, Args: []string{fmt.Sprint(amount)},
		CollapseID: "game:" + gameID, ThreadID: gameID,
		Payload: map[string]any{"game_id": gameID, "kind": KindLowTime},
	}}})
}

// ChallengeReceived tells someone they were invited by name (a rematch, mostly).
func (n *Notifier) ChallengeReceived(inviteeID, fromName, code string) {
	if !n.Enabled() {
		return
	}
	n.enqueue(job{gameID: code, direct: &directJob{userID: inviteeID, kind: KindInvite, note: push.Notification{
		Title: "Lời mời mới", Body: fmt.Sprintf("%s mời bạn một ván cờ.", fromName),
		TitleKey: "push.challenge.title", BodyKey: "push.challenge.body", Args: []string{fromName},
		CollapseID: "invite:" + code, ThreadID: code,
		Payload: map[string]any{"invite_code": code, "kind": KindInvite},
	}}})
}

// FriendRequestReceived tells someone that a player wants to be friends. The
// payload has no game, so the app opens the friends screen instead.
func (n *Notifier) FriendRequestReceived(targetID, fromName, fromID string) {
	if !n.Enabled() {
		return
	}
	n.enqueue(job{gameID: fromID, direct: &directJob{userID: targetID, kind: KindFriend, note: push.Notification{
		Title: "Lời mời kết bạn", Body: fmt.Sprintf("%s muốn kết bạn với bạn.", fromName),
		TitleKey: "push.friend.request.title", BodyKey: "push.friend.request.body",
		Args:       []string{fromName},
		CollapseID: "friend:" + fromID, ThreadID: "friends",
		Payload: map[string]any{"kind": KindFriend, "friend_id": fromID},
	}}})
}

// FriendRequestAccepted closes the loop for the person who asked.
func (n *Notifier) FriendRequestAccepted(targetID, byName, byID string) {
	if !n.Enabled() {
		return
	}
	n.enqueue(job{gameID: byID, direct: &directJob{userID: targetID, kind: KindFriend, note: push.Notification{
		Title: "Đã là bạn bè", Body: fmt.Sprintf("%s đã nhận lời mời kết bạn.", byName),
		TitleKey: "push.friend.accepted.title", BodyKey: "push.friend.accepted.body",
		Args:       []string{byName},
		CollapseID: "friend:" + byID, ThreadID: "friends",
		Payload: map[string]any{"kind": KindFriend, "friend_id": byID},
	}}})
}

// Run drains the queue until ctx ends. One worker is plenty: APNs is fast and
// the queue absorbs bursts.
func (n *Notifier) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-n.queue:
			n.process(ctx, j)
		}
	}
}

func (n *Notifier) process(ctx context.Context, j job) {
	if j.direct != nil {
		n.deliver(ctx, j.direct.userID, j.direct.kind, j.direct.note)
		return
	}
	participants, err := n.config.Games.Participants(ctx, j.gameID)
	if err != nil {
		n.config.Logger.Warn("push: loading participants", "game_id", j.gameID, "error", err)
		return
	}
	for _, event := range j.events {
		switch e := event.(type) {
		case game.MoveMade:
			// Fast games are played with the app open; only slow ones need a nudge.
			if e.Duplicate || !participants.IsCorrespondence {
				continue
			}
			// Low priority: a days-per-move game can wait for the next radio wake-up.
			n.deliver(ctx, userOf(participants, e.By.Opponent()), KindTurn, push.Notification{
				Title: "Đến lượt bạn", Body: fmt.Sprintf("%s vừa đi nước %d.", nameOf(participants, e.By), e.MoveNumber),
				TitleKey: "push.turn.title", BodyKey: "push.turn.body",
				Args:       []string{nameOf(participants, e.By), fmt.Sprint(e.MoveNumber)},
				CollapseID: "game:" + j.gameID, ThreadID: j.gameID, Priority: 5,
				Payload: map[string]any{"game_id": j.gameID, "kind": KindTurn},
			})
		case game.GameEnded:
			for _, colour := range []rules.Color{rules.Black, rules.White} {
				bodyKey, args := endKey(e.Result, colour)
				n.deliver(ctx, userOf(participants, colour), KindGameEnd, push.Notification{
					Title: "Ván đã kết thúc", Body: describe(e.Result, colour),
					TitleKey: "push.end.title", BodyKey: bodyKey, Args: args,
					CollapseID: "game:" + j.gameID, ThreadID: j.gameID,
					Payload: map[string]any{"game_id": j.gameID, "kind": KindGameEnd},
				})
			}
		}
	}
}

func (n *Notifier) deliver(ctx context.Context, userID, kind string, note push.Notification) {
	if userID == "" {
		return
	}
	devices, err := n.config.Devices.ForUser(ctx, userID, kind)
	if err != nil {
		n.config.Logger.Warn("push: listing devices", "user_id", userID, "error", err)
		return
	}
	for _, device := range devices {
		sender := n.senderFor(device.Environment)
		if sender == nil {
			continue
		}
		err := sender.Send(ctx, device.Token, note)
		switch {
		case errors.Is(err, push.ErrUnregistered):
			_ = n.config.Devices.Unregister(ctx, device.Token)
		case err != nil:
			n.config.Logger.Warn("push: sending", "user_id", userID, "error", err)
		}
	}
}

func (n *Notifier) senderFor(environment string) Sender {
	if environment == "sandbox" {
		return n.config.Sandbox
	}
	return n.config.Production
}

func userOf(p store.Participants, colour rules.Color) string {
	if colour == rules.Black {
		return p.BlackUserID
	}
	return p.WhiteUserID
}

func nameOf(p store.Participants, colour rules.Color) string {
	name := p.WhiteName
	if colour == rules.Black {
		name = p.BlackName
	}
	if name == "" {
		return "Đối thủ"
	}
	return name
}

var reasonText = map[rules.EndReason]string{
	rules.ReasonCounting:    "khi đếm điểm",
	rules.ReasonResignation: "do đầu hàng",
	rules.ReasonTimeout:     "do hết giờ",
	rules.ReasonRepetition:  "do lặp thế cờ",
	rules.ReasonAbandonment: "do bỏ ván",
	rules.ReasonMutualDraw:  "theo thỏa thuận",
}

// endKey names the catalog entry for a result seen from one side, e.g.
// push.end.win.resignation; the counting variants take the two totals.
func endKey(result rules.Result, me rules.Color) (string, []string) {
	outcome := "draw"
	switch result.Winner {
	case me:
		outcome = "win"
	case me.Opponent():
		outcome = "lose"
	}
	reason := string(result.Reason)
	if _, known := reasonText[result.Reason]; !known {
		reason = "other"
	}
	key := "push.end." + outcome + "." + reason
	if result.Score != nil && result.Reason == rules.ReasonCounting {
		return key, []string{fmt.Sprintf("%.1f", result.Score.Black), fmt.Sprintf("%.1f", result.Score.White)}
	}
	return key, nil
}

// describe words the result from one player's side.
func describe(result rules.Result, me rules.Color) string {
	outcome := "Hòa"
	switch result.Winner {
	case me:
		outcome = "Bạn thắng"
	case me.Opponent():
		outcome = "Bạn thua"
	}
	if reason, ok := reasonText[result.Reason]; ok {
		outcome += " " + reason
	}
	if result.Score != nil && result.Reason == rules.ReasonCounting {
		outcome += fmt.Sprintf(" (%.1f – %.1f)", result.Score.Black, result.Score.White)
	}
	return outcome + "."
}
