package notify

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"sente.app/server/internal/game"
	"sente.app/server/internal/push"
	"sente.app/server/internal/rules"
	"sente.app/server/internal/store"
)

type sent struct {
	token string
	note  push.Notification
}

type fakeSender struct {
	mu   sync.Mutex
	sent []sent
	fail error
	got  chan sent
}

func newSender() *fakeSender { return &fakeSender{got: make(chan sent, 16)} }

func (f *fakeSender) Send(_ context.Context, token string, n push.Notification) error {
	f.mu.Lock()
	f.sent = append(f.sent, sent{token, n})
	f.mu.Unlock()
	f.got <- sent{token, n}
	return f.fail
}

type fakeDir struct{ p store.Participants }

func (d fakeDir) Participants(context.Context, string) (store.Participants, error) { return d.p, nil }

type fakeDevices struct {
	mu           sync.Mutex
	byUser       map[string][]store.Device
	kinds        []string
	unregistered []string
}

func (d *fakeDevices) ForUser(_ context.Context, userID, kind string) ([]store.Device, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.kinds = append(d.kinds, kind)
	return d.byUser[userID], nil
}

func (d *fakeDevices) Unregister(_ context.Context, token string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.unregistered = append(d.unregistered, token)
	return nil
}

var players = store.Participants{
	BlackUserID: "u-black", WhiteUserID: "u-white", BlackName: "an", WhiteName: "binh", IsCorrespondence: true,
}

func start(t *testing.T, dir Directory, devices Devices) (*Notifier, *fakeSender, *fakeSender) {
	t.Helper()
	sandbox, production := newSender(), newSender()
	n := New(Config{Games: dir, Devices: devices, Sandbox: sandbox, Production: production,
		Logger: slog.New(slog.NewTextHandler(testWriter{t}, nil))})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go n.Run(ctx)
	return n, sandbox, production
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func receive(t *testing.T, s *fakeSender) sent {
	t.Helper()
	select {
	case got := <-s.got:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("no push arrived")
		return sent{}
	}
}

func quiet(t *testing.T, s *fakeSender) {
	t.Helper()
	select {
	case got := <-s.got:
		t.Fatalf("unexpected push: %+v", got)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestAMoveInASlowGameNudgesTheOtherPlayerOnly(t *testing.T) {
	devices := &fakeDevices{byUser: map[string][]store.Device{
		"u-black": {{Token: "tb", Environment: "production"}},
		"u-white": {{Token: "tw", Environment: "production"}},
	}}
	n, sandbox, production := start(t, fakeDir{players}, devices)

	n.Broadcast("g1", []game.Event{game.MoveMade{MoveNumber: 12, By: rules.Black}})

	got := receive(t, production)
	if got.token != "tw" {
		t.Errorf("white should be told, got token %s", got.token)
	}
	if got.note.Title != "Đến lượt bạn" || !strings.Contains(got.note.Body, "an") || !strings.Contains(got.note.Body, "12") {
		t.Errorf("note: %+v", got.note)
	}
	if got.note.TitleKey != "push.turn.title" || got.note.BodyKey != "push.turn.body" || len(got.note.Args) != 2 || got.note.Args[1] != "12" {
		t.Errorf("localisation keys: %+v", got.note)
	}
	// One collapse id per game: a later "game over" replaces a stale "your turn".
	if got.note.CollapseID != "game:g1" || got.note.Payload["game_id"] != "g1" || got.note.Priority != 5 {
		t.Errorf("collapse/payload/priority: %+v", got.note)
	}
	quiet(t, production)
	quiet(t, sandbox)
	if devices.kinds[0] != KindTurn {
		t.Errorf("devices should be filtered by the %q preference, got %q", KindTurn, devices.kinds[0])
	}
}

func TestFastGamesAndReplayedMovesSendNothing(t *testing.T) {
	devices := &fakeDevices{byUser: map[string][]store.Device{"u-white": {{Token: "tw", Environment: "production"}}}}
	fast := players
	fast.IsCorrespondence = false
	n, _, production := start(t, fakeDir{fast}, devices)
	n.Broadcast("g1", []game.Event{game.MoveMade{MoveNumber: 1, By: rules.Black}})
	quiet(t, production)

	n2, _, production2 := start(t, fakeDir{players}, devices)
	n2.Broadcast("g1", []game.Event{game.MoveMade{MoveNumber: 1, By: rules.Black, Duplicate: true}})
	quiet(t, production2)
}

func TestGameOverReachesBothSidesInTheirOwnWords(t *testing.T) {
	devices := &fakeDevices{byUser: map[string][]store.Device{
		"u-black": {{Token: "tb", Environment: "sandbox"}},
		"u-white": {{Token: "tw", Environment: "production"}},
	}}
	n, sandbox, production := start(t, fakeDir{players}, devices)
	n.Broadcast("g1", []game.Event{game.GameEnded{Result: rules.Result{Winner: rules.White, Reason: rules.ReasonResignation}}})

	black := receive(t, sandbox)
	white := receive(t, production)
	if black.token != "tb" || !strings.HasPrefix(black.note.Body, "Bạn thua") {
		t.Errorf("black: %+v", black)
	}
	if white.token != "tw" || !strings.HasPrefix(white.note.Body, "Bạn thắng") {
		t.Errorf("white: %+v", white)
	}
}

func TestEndKeysNameOutcomeAndReason(t *testing.T) {
	score := &rules.Score{Black: 40.5, White: 38}
	key, args := endKey(rules.Result{Winner: rules.Black, Reason: rules.ReasonCounting, Score: score}, rules.Black)
	if key != "push.end.win.counting" || len(args) != 2 || args[0] != "40.5" || args[1] != "38.0" {
		t.Errorf("counting: %s %v", key, args)
	}
	if key, args := endKey(rules.Result{Winner: rules.Black, Reason: rules.ReasonTimeout}, rules.White); key != "push.end.lose.timeout" || args != nil {
		t.Errorf("timeout: %s %v", key, args)
	}
	if key, _ := endKey(rules.Result{Reason: rules.ReasonMutualDraw}, rules.Black); key != "push.end.draw.mutual_draw" {
		t.Errorf("draw: %s", key)
	}
	if key, _ := endKey(rules.Result{Winner: rules.Black, Reason: "weird"}, rules.Black); key != "push.end.win.other" {
		t.Errorf("unknown reason must map to a key the app has: %s", key)
	}
}

func TestDescribeCoversDrawsAndScores(t *testing.T) {
	score := &rules.Score{Black: 40.5, White: 38}
	cases := map[string]string{
		describe(rules.Result{Winner: rules.Black, Reason: rules.ReasonCounting, Score: score}, rules.Black): "Bạn thắng khi đếm điểm (40.5 – 38.0).",
		describe(rules.Result{Reason: rules.ReasonMutualDraw}, rules.Black):                                  "Hòa theo thỏa thuận.",
		describe(rules.Result{Winner: rules.Black, Reason: rules.ReasonTimeout}, rules.White):                "Bạn thua do hết giờ.",
		describe(rules.Result{Winner: rules.Black, Reason: "weird"}, rules.Black):                            "Bạn thắng.",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("want %q, got %q", want, got)
		}
	}
}

func TestADeadTokenIsForgotten(t *testing.T) {
	devices := &fakeDevices{byUser: map[string][]store.Device{"u-black": {{Token: "dead", Environment: "production"}}}}
	sandbox, production := newSender(), newSender()
	production.fail = push.ErrUnregistered
	n := New(Config{Games: fakeDir{players}, Devices: devices, Sandbox: sandbox, Production: production})
	n.process(context.Background(), job{gameID: "g1", events: []game.Event{game.MoveMade{MoveNumber: 3, By: rules.White}}})
	if len(devices.unregistered) != 1 || devices.unregistered[0] != "dead" {
		t.Errorf("dead token should be unregistered, got %v", devices.unregistered)
	}

	// Any other failure is logged and the token kept.
	devices.unregistered = nil
	production.fail = errors.New("503")
	n.process(context.Background(), job{gameID: "g1", events: []game.Event{game.MoveMade{MoveNumber: 4, By: rules.White}}})
	if len(devices.unregistered) != 0 {
		t.Error("a transient failure must not drop the token")
	}
}

func TestInvitationAcceptedGoesToTheCreator(t *testing.T) {
	devices := &fakeDevices{byUser: map[string][]store.Device{"creator": {{Token: "tc", Environment: "production"}}}}
	n, _, production := start(t, fakeDir{players}, devices)
	n.InvitationAccepted("creator", "binh", "g9")
	got := receive(t, production)
	if got.token != "tc" || !strings.Contains(got.note.Body, "binh") || got.note.Payload["game_id"] != "g9" {
		t.Errorf("got %+v", got)
	}
	if devices.kinds[0] != KindInvite {
		t.Errorf("kind %q", devices.kinds[0])
	}
}

func TestWithoutAnyClientEverythingIsANoOp(t *testing.T) {
	var nilNotifier *Notifier
	if nilNotifier.Enabled() {
		t.Error("nil must read as disabled")
	}
	nilNotifier.Broadcast("g", nil)
	nilNotifier.InvitationAccepted("u", "x", "g")

	n := New(Config{Games: fakeDir{players}, Devices: &fakeDevices{}})
	if n.Enabled() {
		t.Error("no senders means disabled")
	}
	n.Broadcast("g1", []game.Event{game.GameEnded{}})
	if len(n.queue) != 0 {
		t.Error("disabled notifier must not queue")
	}
}

func TestAFullQueueDropsRatherThanBlocks(t *testing.T) {
	n := New(Config{Games: fakeDir{players}, Devices: &fakeDevices{}, Production: newSender(), QueueSize: 1})
	done := make(chan struct{})
	go func() {
		for range 5 {
			n.Broadcast("g", []game.Event{game.GameEnded{}})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Broadcast blocked on a full queue")
	}
}

func TestMissingPlayerOrDeviceIsSkipped(t *testing.T) {
	sender := newSender()
	n := New(Config{Games: fakeDir{store.Participants{BlackUserID: "u-black", IsCorrespondence: true}},
		Devices:    &fakeDevices{byUser: map[string][]store.Device{"u-black": {{Token: "t", Environment: "sandbox"}}}},
		Production: sender})
	// White seat empty: nothing to send. Black on sandbox with no sandbox client: skipped.
	n.process(context.Background(), job{gameID: "g", events: []game.Event{
		game.MoveMade{MoveNumber: 1, By: rules.Black}, game.GameEnded{}}})
	if len(sender.sent) != 0 {
		t.Errorf("nothing should be sent, got %+v", sender.sent)
	}
}
