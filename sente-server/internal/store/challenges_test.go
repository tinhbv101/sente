package store

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"sente.app/server/internal/game"
	"sente.app/server/internal/rules"
)

func newChallenges(t *testing.T) *Challenges {
	t.Helper()
	skipIfShort(t)
	return NewChallenges(testPool)
}

func seedUser(t *testing.T) string {
	t.Helper()
	user, err := NewUsers(testPool).CreateGuest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return user.ID
}

func openInvite(t *testing.T, challenges *Challenges, creator string) Challenge {
	t.Helper()
	challenge, err := challenges.Create(context.Background(), CreateChallengeParams{
		CreatorID: creator, Config: blitzConfig(), CreatorColor: "black",
	})
	if err != nil {
		t.Fatal(err)
	}
	return challenge
}

func TestAnInvitationCarriesItsConfiguration(t *testing.T) {
	challenges := newChallenges(t)
	ctx := context.Background()
	creator := seedUser(t)

	config := blitzConfig()
	config.Size = 13
	config.Rules = rules.Chinese
	config.Komi = 7.5
	config.TimeControl = game.TimeControl{Kind: game.Byoyomi, MainTime: 20 * time.Minute,
		Periods: 3, PeriodTime: 30 * time.Second}

	created, err := challenges.Create(ctx, CreateChallengeParams{
		CreatorID: creator, Config: config, CreatorColor: "white",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Code) != 8 {
		t.Fatalf("want an eight-character code, got %q", created.Code)
	}
	if strings.ContainsAny(created.Code, "IO01") {
		t.Errorf("the code must avoid characters that are read wrong: %q", created.Code)
	}

	fetched, err := challenges.ByCode(ctx, created.Code)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.Config.Size != 13 || fetched.Config.Rules != rules.Chinese || fetched.Config.Komi != 7.5 {
		t.Errorf("configuration lost: %+v", fetched.Config)
	}
	if fetched.Config.TimeControl.Periods != 3 ||
		fetched.Config.TimeControl.MainTime != 20*time.Minute {
		t.Errorf("time control lost: %+v", fetched.Config.TimeControl)
	}
	if fetched.CreatorName == "" {
		t.Error("the preview must name who is inviting")
	}
}

func TestAcceptingCreatesAPlayableGame(t *testing.T) {
	challenges := newChallenges(t)
	ctx := context.Background()
	creator, invitee := seedUser(t), seedUser(t)
	invite := openInvite(t, challenges, creator)

	accepted, err := challenges.Accept(ctx, invite.Code, invitee)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.GameID == "" {
		t.Fatal("accepting must produce a game")
	}
	loaded, err := NewGames(testPool).Load(ctx, accepted.GameID)
	if err != nil {
		t.Fatal(err)
	}
	// The creator asked for black, so that is what they get.
	if loaded.BlackUserID != creator || loaded.WhiteUserID != invitee {
		t.Errorf("seats wrong: black=%s white=%s", loaded.BlackUserID, loaded.WhiteUserID)
	}
	if loaded.Session.Phase() != rules.Playing {
		t.Errorf("the game should be playable at once, got %v", loaded.Session.Phase())
	}
	// And the invitation is closed, pointing at the game it became.
	after, err := challenges.ByCode(ctx, invite.Code)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != ChallengeAccepted || after.GameID != accepted.GameID {
		t.Errorf("invitation not closed properly: %+v", after)
	}
}

func TestChoosingWhiteSwapsTheSeats(t *testing.T) {
	challenges := newChallenges(t)
	ctx := context.Background()
	creator, invitee := seedUser(t), seedUser(t)

	invite, err := challenges.Create(ctx, CreateChallengeParams{
		CreatorID: creator, Config: blitzConfig(), CreatorColor: "white",
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := challenges.Accept(ctx, invite.Code, invitee)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ := NewGames(testPool).Load(ctx, accepted.GameID)
	if loaded.WhiteUserID != creator || loaded.BlackUserID != invitee {
		t.Errorf("seats wrong: black=%s white=%s", loaded.BlackUserID, loaded.WhiteUserID)
	}
}

// Random must actually be random, and must be decided when the invitation is
// accepted rather than when it is created, so neither side can see it coming.
func TestRandomColourIsDecidedOnAcceptance(t *testing.T) {
	challenges := newChallenges(t)
	ctx := context.Background()
	creatorWasBlack := 0

	for i := 0; i < 40; i++ {
		creator, invitee := seedUser(t), seedUser(t)
		invite, err := challenges.Create(ctx, CreateChallengeParams{
			CreatorID: creator, Config: blitzConfig(), CreatorColor: "random",
		})
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := challenges.Accept(ctx, invite.Code, invitee)
		if err != nil {
			t.Fatal(err)
		}
		loaded, _ := NewGames(testPool).Load(ctx, accepted.GameID)
		if loaded.BlackUserID == creator {
			creatorWasBlack++
		}
	}
	// Forty coin flips landing 0-3 or 37-40 times has probability under 1e-8.
	if creatorWasBlack < 4 || creatorWasBlack > 36 {
		t.Errorf("colour does not look random: creator was black %d of 40 times", creatorWasBlack)
	}
}

// Two people opening one link at the same instant is the normal case. Exactly one
// may end up in a game.
func TestOnlyOnePersonCanAcceptAnOpenLink(t *testing.T) {
	challenges := newChallenges(t)
	ctx := context.Background()
	creator := seedUser(t)
	invite := openInvite(t, challenges, creator)

	// Hold every racer inside the critical section. Without this the first
	// transaction commits in a millisecond and the others never overlap it, so a
	// missing row lock passes the test by luck. Verified: removing FOR UPDATE with
	// this pause in place turns one winner into many.
	afterAcceptLock = func() { time.Sleep(150 * time.Millisecond) }
	defer func() { afterAcceptLock = func() {} }()

	const racers = 12
	users := make([]string, racers)
	for i := range users {
		users[i] = seedUser(t)
	}

	var mu sync.Mutex
	var wins []string
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, user := range users {
		wg.Add(1)
		go func(user string) {
			defer wg.Done()
			<-start
			if accepted, err := challenges.Accept(ctx, invite.Code, user); err == nil {
				mu.Lock()
				wins = append(wins, accepted.GameID)
				mu.Unlock()
			}
		}(user)
	}
	close(start)
	wg.Wait()

	if len(wins) != 1 {
		t.Fatalf("want exactly one winner, got %d", len(wins))
	}
	// And exactly one game exists for that invitation.
	var games int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM challenges WHERE code = $1 AND game_id IS NOT NULL`,
		invite.Code).Scan(&games); err != nil {
		t.Fatal(err)
	}
	if games != 1 {
		t.Errorf("want one game linked, got %d", games)
	}
}

func TestTheCreatorCannotAcceptTheirOwnInvitation(t *testing.T) {
	challenges := newChallenges(t)
	creator := seedUser(t)
	invite := openInvite(t, challenges, creator)

	if _, err := challenges.Accept(context.Background(), invite.Code, creator); err != ErrOwnChallenge {
		t.Errorf("want ErrOwnChallenge, got %v", err)
	}
}

func TestADirectedInvitationIsOnlyForItsInvitee(t *testing.T) {
	challenges := newChallenges(t)
	ctx := context.Background()
	creator, invitee, stranger := seedUser(t), seedUser(t), seedUser(t)

	invite, err := challenges.Create(ctx, CreateChallengeParams{
		CreatorID: creator, InviteeID: invitee, Config: blitzConfig(), CreatorColor: "black",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := challenges.Accept(ctx, invite.Code, stranger); err != ErrChallengeGone {
		t.Errorf("a stranger must not take a directed invitation, got %v", err)
	}
	if _, err := challenges.Accept(ctx, invite.Code, invitee); err != nil {
		t.Errorf("the invitee must be able to accept: %v", err)
	}
}

func TestAnExpiredInvitationCannotBeAccepted(t *testing.T) {
	challenges := newChallenges(t)
	ctx := context.Background()
	creator, invitee := seedUser(t), seedUser(t)

	invite, err := challenges.Create(ctx, CreateChallengeParams{
		CreatorID: creator, Config: blitzConfig(), CreatorColor: "black",
		TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx,
		`UPDATE challenges SET expires_at = now() - interval '1 second' WHERE code = $1`,
		invite.Code); err != nil {
		t.Fatal(err)
	}

	if _, err := challenges.Accept(ctx, invite.Code, invitee); err != ErrChallengeGone {
		t.Errorf("want ErrChallengeGone, got %v", err)
	}
	// Reading it must show expired, without waiting for the sweeper.
	fetched, err := challenges.ByCode(ctx, invite.Code)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.Status != ChallengeExpired {
		t.Errorf("want expired on read, got %q", fetched.Status)
	}
	// And the sweeper writes it down.
	swept, err := challenges.ExpireStale(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if swept < 1 {
		t.Error("the sweeper should have closed at least this one")
	}
}

func TestDecliningAndCancelling(t *testing.T) {
	challenges := newChallenges(t)
	ctx := context.Background()
	creator, invitee := seedUser(t), seedUser(t)

	declined := openInvite(t, challenges, creator)
	if err := challenges.Decline(ctx, declined.Code, invitee); err != nil {
		t.Fatal(err)
	}
	if _, err := challenges.Accept(ctx, declined.Code, invitee); err != ErrChallengeGone {
		t.Errorf("a declined invitation cannot be accepted, got %v", err)
	}

	cancelled := openInvite(t, challenges, creator)
	// Only the creator may cancel.
	if err := challenges.Cancel(ctx, cancelled.Code, invitee); err != ErrChallengeGone {
		t.Errorf("only the creator may cancel, got %v", err)
	}
	if err := challenges.Cancel(ctx, cancelled.Code, creator); err != nil {
		t.Fatal(err)
	}
	if _, err := challenges.Accept(ctx, cancelled.Code, invitee); err != ErrChallengeGone {
		t.Errorf("a cancelled invitation cannot be accepted, got %v", err)
	}
}

// An unknown code and a used code must look identical, or the endpoint becomes a
// way to find out which codes exist.
func TestAnUnknownCodeIsIndistinguishableFromAUsedOne(t *testing.T) {
	challenges := newChallenges(t)
	ctx := context.Background()
	creator, invitee := seedUser(t), seedUser(t)
	used := openInvite(t, challenges, creator)
	if _, err := challenges.Accept(ctx, used.Code, invitee); err != nil {
		t.Fatal(err)
	}

	_, usedErr := challenges.Accept(ctx, used.Code, seedUser(t))
	_, unknownErr := challenges.Accept(ctx, "ZZZZZZZZ", seedUser(t))
	if usedErr != ErrChallengeGone || unknownErr != ErrChallengeGone {
		t.Errorf("the two must be the same error: used=%v unknown=%v", usedErr, unknownErr)
	}
}

func TestListingShowsBothDirections(t *testing.T) {
	challenges := newChallenges(t)
	ctx := context.Background()
	creator, invitee := seedUser(t), seedUser(t)

	sent := openInvite(t, challenges, creator)
	received, err := challenges.Create(ctx, CreateChallengeParams{
		CreatorID: invitee, InviteeID: creator, Config: blitzConfig(), CreatorColor: "black",
	})
	if err != nil {
		t.Fatal(err)
	}

	list, err := challenges.ListForUser(ctx, creator)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, challenge := range list {
		codes[challenge.Code] = true
	}
	if !codes[sent.Code] || !codes[received.Code] {
		t.Errorf("want both directions listed, got %v", codes)
	}
}

func TestCreateRejectsBadInput(t *testing.T) {
	challenges := newChallenges(t)
	ctx := context.Background()
	creator := seedUser(t)

	broken := blitzConfig()
	broken.TimeControl = game.TimeControl{Kind: game.Byoyomi, MainTime: time.Minute}
	if _, err := challenges.Create(ctx, CreateChallengeParams{
		CreatorID: creator, Config: broken, CreatorColor: "black"}); err == nil {
		t.Error("an unplayable time control must not become an invitation")
	}
	if _, err := challenges.Create(ctx, CreateChallengeParams{
		CreatorID: creator, Config: blitzConfig(), CreatorColor: "purple"}); err == nil {
		t.Error("an unknown colour choice must be refused")
	}
	// Defaulting to random keeps the API forgiving where it can be.
	if _, err := challenges.Create(ctx, CreateChallengeParams{
		CreatorID: creator, Config: blitzConfig()}); err != nil {
		t.Errorf("an unspecified colour should default to random: %v", err)
	}
}

func TestDatabaseFailuresArePropagatedForChallenges(t *testing.T) {
	challenges := newChallenges(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	creator := "00000000-0000-0000-0000-000000000000"

	if _, err := challenges.Create(cancelled, CreateChallengeParams{
		CreatorID: creator, Config: blitzConfig(), CreatorColor: "black"}); err == nil {
		t.Error("Create must report a failed write")
	}
	if _, err := challenges.ByCode(cancelled, "AAAAAAAA"); err == nil {
		t.Error("ByCode must report a failed read")
	}
	if _, err := challenges.Accept(cancelled, "AAAAAAAA", creator); err == nil {
		t.Error("Accept must report a failed write")
	}
	if err := challenges.Decline(cancelled, "AAAAAAAA", creator); err == nil {
		t.Error("Decline must report a failed write")
	}
	if _, err := challenges.ListForUser(cancelled, creator); err == nil {
		t.Error("ListForUser must report a failed read")
	}
	if _, err := challenges.ExpireStale(cancelled); err == nil {
		t.Error("ExpireStale must report a failed write")
	}
}
