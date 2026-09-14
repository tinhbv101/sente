package store

import (
	"context"
	"errors"
	"testing"
)

// befriendable makes an account that has signed in with Apple, which is the only
// kind that may have friends.
func befriendable(t *testing.T, subject string) User {
	t.Helper()
	ctx := context.Background()
	users := NewUsers(testPool)
	user, err := users.CreateGuest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewIdentities(testPool).Link(ctx, user.ID, ProviderApple, subject, ""); err != nil {
		t.Fatal(err)
	}
	user.IsGuest = false
	return user
}

func TestFriendRequestAndAnswer(t *testing.T) {
	skipIfShort(t)
	ctx := context.Background()
	friends := NewFriends(testPool)
	an := befriendable(t, "friend-an-"+NewID())
	binh := befriendable(t, "friend-binh-"+NewID())

	outcome, err := friends.Request(ctx, an.ID, binh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != FriendPending || !outcome.Created {
		t.Fatalf("first request: %+v", outcome)
	}

	// Asking again changes nothing and must not look like a fresh request, or
	// every repeat would ring the other phone.
	again, err := friends.Request(ctx, an.ID, binh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != FriendPending || again.Created {
		t.Errorf("repeat request: %+v", again)
	}

	// The invitee sees it as incoming; the sender does not.
	incoming, err := friends.List(ctx, binh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(incoming) != 1 || !incoming[0].Incoming || incoming[0].UserID != an.ID {
		t.Fatalf("binh's list: %+v", incoming)
	}
	outgoing, _ := friends.List(ctx, an.ID)
	if len(outgoing) != 1 || outgoing[0].Incoming {
		t.Fatalf("an's list: %+v", outgoing)
	}

	if err := friends.Accept(ctx, binh.ID, an.ID); err != nil {
		t.Fatal(err)
	}
	for _, side := range []User{an, binh} {
		list, _ := friends.List(ctx, side.ID)
		if len(list) != 1 || list[0].Status != FriendAccepted || list[0].Incoming {
			t.Errorf("%s after accept: %+v", side.ID, list)
		}
	}
	if !areFriends(t, friends, an.ID, binh.ID) {
		t.Error("they should be friends")
	}
}

// areFriends reads the pair back the way the app does.
func areFriends(t *testing.T, friends *Friends, userID, otherID string) bool {
	t.Helper()
	list, err := friends.List(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, friend := range list {
		if friend.UserID == otherID {
			return friend.Status == FriendAccepted
		}
	}
	return false
}

// Both asking at once is agreement, not a conflict: the second request accepts
// the first. The canonical pair makes the second insert collide by construction.
func TestCrossedRequestsBecomeAFriendship(t *testing.T) {
	skipIfShort(t)
	ctx := context.Background()
	friends := NewFriends(testPool)
	an := befriendable(t, "crossed-an-"+NewID())
	binh := befriendable(t, "crossed-binh-"+NewID())

	if _, err := friends.Request(ctx, an.ID, binh.ID); err != nil {
		t.Fatal(err)
	}
	outcome, err := friends.Request(ctx, binh.ID, an.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != FriendAccepted {
		t.Fatalf("crossed requests should accept, got %+v", outcome)
	}
	if !areFriends(t, friends, an.ID, binh.ID) {
		t.Error("they should be friends")
	}
}

// A refusal sticks: the refused person cannot ask again, and cannot clear the
// tombstone by "cancelling" it either. The person who refused can still change
// their mind by asking themselves.
func TestDeclineIsStickyButReversibleByTheDecliner(t *testing.T) {
	skipIfShort(t)
	ctx := context.Background()
	friends := NewFriends(testPool)
	an := befriendable(t, "declined-an-"+NewID())
	binh := befriendable(t, "declined-binh-"+NewID())

	if _, err := friends.Request(ctx, an.ID, binh.ID); err != nil {
		t.Fatal(err)
	}
	if err := friends.Decline(ctx, binh.ID, an.ID); err != nil {
		t.Fatal(err)
	}
	// Neither side sees a declined row.
	for _, side := range []User{an, binh} {
		if list, _ := friends.List(ctx, side.ID); len(list) != 0 {
			t.Errorf("%s should see nothing, got %+v", side.ID, list)
		}
	}
	// Asking again reports as sent but writes nothing.
	outcome, err := friends.Request(ctx, an.ID, binh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != FriendPending || outcome.Created {
		t.Errorf("a refused resend must look sent but be a no-op: %+v", outcome)
	}
	if list, _ := friends.List(ctx, binh.ID); len(list) != 0 {
		t.Errorf("the refusal must hold: %+v", list)
	}
	// And the refused party cannot delete the tombstone to get a fresh start.
	if err := friends.Remove(ctx, an.ID, binh.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing a refusal: want ErrNotFound, got %v", err)
	}

	// The decliner asking is a new request, and revives the row.
	revived, err := friends.Request(ctx, binh.ID, an.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revived.Status != FriendPending {
		t.Fatalf("the decliner's own request: %+v", revived)
	}
	if list, _ := friends.List(ctx, an.ID); len(list) != 1 || !list[0].Incoming {
		t.Errorf("an should now have an incoming request: %+v", list)
	}
}

func TestFriendsNeedAnAppleAccount(t *testing.T) {
	skipIfShort(t)
	ctx := context.Background()
	friends := NewFriends(testPool)
	users := NewUsers(testPool)

	guest, err := users.CreateGuest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	linked := befriendable(t, "guestrule-"+NewID())

	if _, err := friends.Request(ctx, guest.ID, linked.ID); !errors.Is(err, ErrGuestAccount) {
		t.Errorf("a guest asking: want ErrGuestAccount, got %v", err)
	}
	if _, err := friends.Request(ctx, linked.ID, guest.ID); !errors.Is(err, ErrNotFriendable) {
		t.Errorf("asking a guest: want ErrNotFriendable, got %v", err)
	}
	if _, err := friends.Request(ctx, linked.ID, linked.ID); !errors.Is(err, ErrNotFriendable) {
		t.Errorf("asking yourself: want ErrNotFriendable, got %v", err)
	}
}

// Blocking is the end of it: the friendship goes, a request from the blocked
// person reports as sent but writes nothing, and their code stops resolving.
func TestBlockingEndsAFriendship(t *testing.T) {
	skipIfShort(t)
	ctx := context.Background()
	friends := NewFriends(testPool)
	moderation := NewModeration(testPool)
	an := befriendable(t, "block-an-"+NewID())
	binh := befriendable(t, "block-binh-"+NewID())

	if _, err := friends.Request(ctx, an.ID, binh.ID); err != nil {
		t.Fatal(err)
	}
	if err := friends.Accept(ctx, binh.ID, an.ID); err != nil {
		t.Fatal(err)
	}
	if err := moderation.Block(ctx, binh.ID, an.ID); err != nil {
		t.Fatal(err)
	}
	if areFriends(t, friends, an.ID, binh.ID) {
		t.Error("blocking must end the friendship")
	}
	if list, _ := friends.List(ctx, an.ID); len(list) != 0 {
		t.Errorf("the blocked person should see nothing: %+v", list)
	}
	// The block is invisible: the request reports as sent.
	outcome, err := friends.Request(ctx, an.ID, binh.ID)
	if err != nil {
		t.Fatalf("a blocked request must not report an error: %v", err)
	}
	if outcome.Status != FriendPending {
		t.Errorf("a blocked request should look ordinary: %+v", outcome)
	}
	if list, _ := friends.List(ctx, binh.ID); len(list) != 0 {
		t.Errorf("nothing may reach the blocker: %+v", list)
	}
}

// docs/08 §4.2: a blocked code and a code nobody owns answer identically.
func TestLookupByCodeHidesWhoCannotBeFound(t *testing.T) {
	skipIfShort(t)
	ctx := context.Background()
	friends := NewFriends(testPool)
	users := NewUsers(testPool)
	an := befriendable(t, "code-an-"+NewID())
	binh := befriendable(t, "code-binh-"+NewID())

	found, err := friends.ByCode(ctx, an.ID, binh.FriendCode)
	if err != nil || found.ID != binh.ID {
		t.Fatalf("looking up a friend code: %v %+v", err, found)
	}
	if _, err := friends.ByCode(ctx, an.ID, an.FriendCode); !errors.Is(err, ErrNotFound) {
		t.Errorf("your own code is not a lookup: %v", err)
	}

	guest, _ := users.CreateGuest(ctx)
	if _, err := friends.ByCode(ctx, an.ID, guest.FriendCode); !errors.Is(err, ErrNotFound) {
		t.Errorf("a guest's code must not resolve: %v", err)
	}
	if err := NewModeration(testPool).Block(ctx, binh.ID, an.ID); err != nil {
		t.Fatal(err)
	}
	blocked := friends.byCodeError(ctx, an.ID, binh.FriendCode)
	missing := friends.byCodeError(ctx, an.ID, "ZZZZZZZZ")
	if blocked != missing {
		t.Errorf("a blocked code must answer exactly like an unknown one: %q vs %q", blocked, missing)
	}
}

// byCodeError reduces a lookup to the string a caller could observe.
func (f *Friends) byCodeError(ctx context.Context, viewerID, code string) string {
	_, err := f.ByCode(ctx, viewerID, code)
	if err == nil {
		return "found"
	}
	return err.Error()
}

// Deleting an account takes its friendships with it -- the users row survives,
// so no cascade fires on its own.
func TestDeletingAnAccountRemovesItsFriendships(t *testing.T) {
	skipIfShort(t)
	ctx := context.Background()
	friends := NewFriends(testPool)
	users := NewUsers(testPool)
	an := befriendable(t, "gone-an-"+NewID())
	binh := befriendable(t, "gone-binh-"+NewID())

	if _, err := friends.Request(ctx, an.ID, binh.ID); err != nil {
		t.Fatal(err)
	}
	if err := friends.Accept(ctx, binh.ID, an.ID); err != nil {
		t.Fatal(err)
	}
	if err := users.DeleteAccount(ctx, an.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := friends.List(ctx, binh.ID); len(list) != 0 {
		t.Errorf("a deleted friend must leave the list: %+v", list)
	}
	if areFriends(t, friends, binh.ID, an.ID) {
		t.Error("a deleted account has no friends")
	}
}

// Apple revocation demotes the account to a guest but must not destroy what it
// already had: the friendship sleeps until they sign in again.
func TestRevokingAppleKeepsExistingFriendships(t *testing.T) {
	skipIfShort(t)
	ctx := context.Background()
	friends := NewFriends(testPool)
	subject := "revoked-" + NewID()
	an := befriendable(t, subject)
	binh := befriendable(t, "revoked-binh-"+NewID())

	if _, err := friends.Request(ctx, an.ID, binh.ID); err != nil {
		t.Fatal(err)
	}
	if err := friends.Accept(ctx, binh.ID, an.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := NewIdentities(testPool).Unlink(ctx, ProviderApple, subject); err != nil {
		t.Fatal(err)
	}
	if !areFriends(t, friends, binh.ID, an.ID) {
		t.Error("revocation must not delete the friendship")
	}
	// But a guest cannot make new friends.
	other := befriendable(t, "revoked-other-"+NewID())
	if _, err := friends.Request(ctx, an.ID, other.ID); !errors.Is(err, ErrGuestAccount) {
		t.Errorf("a demoted account asking: want ErrGuestAccount, got %v", err)
	}
}

// Only a real transition is worth a push: a new request, a crossed request
// becoming a friendship, a refusal the decliner revived. Asking twice is not.
func TestOnlyRealTransitionsAreReportedAsChanged(t *testing.T) {
	skipIfShort(t)
	ctx := context.Background()
	friends := NewFriends(testPool)
	an := befriendable(t, "changed-an-"+NewID())
	binh := befriendable(t, "changed-binh-"+NewID())

	first, _ := friends.Request(ctx, an.ID, binh.ID)
	if !first.Changed || !first.Created {
		t.Errorf("a new request is a change: %+v", first)
	}
	repeat, _ := friends.Request(ctx, an.ID, binh.ID)
	if repeat.Changed {
		t.Errorf("asking twice changes nothing: %+v", repeat)
	}
	// Binh asking back accepts, and that is a change worth telling An about.
	crossed, _ := friends.Request(ctx, binh.ID, an.ID)
	if !crossed.Changed || crossed.Status != FriendAccepted || crossed.Created {
		t.Errorf("a crossed request: %+v", crossed)
	}
	// Now that they are friends, asking again is not a change.
	settled, _ := friends.Request(ctx, an.ID, binh.ID)
	if settled.Changed {
		t.Errorf("already friends: %+v", settled)
	}
}

// A refusal must survive a block and an unblock. Without that, the refused
// party has a way back in: block, unblock, ask again.
func TestBlockingDoesNotClearARefusal(t *testing.T) {
	skipIfShort(t)
	ctx := context.Background()
	friends := NewFriends(testPool)
	moderation := NewModeration(testPool)
	an := befriendable(t, "tomb-an-"+NewID())
	binh := befriendable(t, "tomb-binh-"+NewID())

	if _, err := friends.Request(ctx, an.ID, binh.ID); err != nil {
		t.Fatal(err)
	}
	if err := friends.Decline(ctx, binh.ID, an.ID); err != nil {
		t.Fatal(err)
	}
	// The refused party blocks and unblocks, hoping for a clean slate.
	if err := moderation.Block(ctx, an.ID, binh.ID); err != nil {
		t.Fatal(err)
	}
	if err := moderation.Unblock(ctx, an.ID, binh.ID); err != nil {
		t.Fatal(err)
	}
	outcome, err := friends.Request(ctx, an.ID, binh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Changed {
		t.Errorf("a block/unblock cycle must not revive a refused request: %+v", outcome)
	}
	if list, _ := friends.List(ctx, binh.ID); len(list) != 0 {
		t.Errorf("the refusal must still hold: %+v", list)
	}
}

// An account demoted to guest (Apple revoked) may clear its inbox, but must not
// form a new friendship by answering yes.
func TestADemotedAccountCannotAcceptButCanDecline(t *testing.T) {
	skipIfShort(t)
	ctx := context.Background()
	friends := NewFriends(testPool)
	subject := "demoted-" + NewID()
	an := befriendable(t, subject)
	binh := befriendable(t, "demoted-other-"+NewID())

	if _, err := friends.Request(ctx, binh.ID, an.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := NewIdentities(testPool).Unlink(ctx, ProviderApple, subject); err != nil {
		t.Fatal(err)
	}
	if err := friends.Accept(ctx, an.ID, binh.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a demoted account accepting: want ErrNotFound, got %v", err)
	}
	if err := friends.Decline(ctx, an.ID, binh.ID); err != nil {
		t.Errorf("a demoted account must still be able to say no: %v", err)
	}
}
