package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Friendship is only for accounts that signed in with Apple: a guest account
// belongs to a phone, not a person, so befriending one promises something the
// server cannot keep across a reinstall (docs/01 FR-A3).
var (
	// ErrGuestAccount: the caller has not signed in with Apple.
	ErrGuestAccount = errors.New("store: friends need a signed-in account")
	// ErrNotFriendable: the other account cannot be befriended -- unknown,
	// deleted, a guest, or yourself. Deliberately one error, so the caller
	// cannot tell those apart (docs/08 §4.2).
	ErrNotFriendable = errors.New("store: this player cannot be added")
)

type FriendStatus string

const (
	FriendPending  FriendStatus = "pending"
	FriendAccepted FriendStatus = "accepted"
	FriendDeclined FriendStatus = "declined"
)

type Friends struct{ pool *pgxpool.Pool }

func NewFriends(pool *pgxpool.Pool) *Friends { return &Friends{pool: pool} }

// Friend is one row of somebody's friends screen, seen from their side.
type Friend struct {
	UserID      string
	DisplayName string
	FriendCode  string
	Status      FriendStatus
	// Incoming is true while they are waiting for *us* to answer.
	Incoming  bool
	CreatedAt time.Time
}

// RequestOutcome says what a request did, so the app can word it correctly and
// only a real change is worth a push.
type RequestOutcome struct {
	Status FriendStatus
	// Created is true only for a pair that had no row at all.
	Created bool
	// Changed covers every state this call actually moved: a new request, a
	// crossed request becoming a friendship, a refusal revived by the person
	// who made it. Asking twice changes nothing.
	Changed bool
}

// blockedPair is reused by every statement here: a block cuts in both
// directions, whoever pressed the button.
const blockedPair = `NOT EXISTS (SELECT 1 FROM blocks
	 WHERE (blocker_id = $1::uuid AND blocked_id = $2::uuid)
	    OR (blocker_id = $2::uuid AND blocked_id = $1::uuid))`

// Request asks someone to be friends, or answers a request already waiting from
// them -- two people asking each other is agreement, not a conflict.
//
// The whole transition is one statement: the authorisation, the guest rule and
// the block check live in its WHERE clause, so nothing can change between the
// check and the write. ON CONFLICT ... DO UPDATE always returns the row from the
// locked tuple, which a plain DO NOTHING plus a follow-up SELECT could not do --
// that read would see the pre-statement snapshot and miss a concurrent insert.
func (f *Friends) Request(ctx context.Context, requesterID, targetID string) (RequestOutcome, error) {
	if requesterID == targetID {
		return RequestOutcome{}, ErrNotFriendable
	}
	var (
		status    FriendStatus
		requester string
		created   bool
		before    *string
	)
	// `before` reads the pre-statement snapshot: a row inserted by a transaction
	// that commits while this statement waits is invisible to it. That is why a
	// missing `before` counts as a change below -- the alternative is a crossed
	// request that silently never tells the other side.
	err := f.pool.QueryRow(ctx, `
		WITH before AS (
			SELECT status FROM friendships
			 WHERE user_low = least($1::uuid, $2::uuid) AND user_high = greatest($1::uuid, $2::uuid)
		), upsert AS (
		INSERT INTO friendships (user_low, user_high, requester_id, status)
		SELECT least($1::uuid, $2::uuid), greatest($1::uuid, $2::uuid), $1::uuid, 'pending'
		 WHERE `+blockedPair+`
		   AND EXISTS (SELECT 1 FROM users WHERE id = $1::uuid AND NOT is_guest AND deleted_at IS NULL)
		   AND EXISTS (SELECT 1 FROM users WHERE id = $2::uuid AND NOT is_guest AND deleted_at IS NULL)
		ON CONFLICT (user_low, user_high) DO UPDATE SET
			-- Asking again changes nothing: a refusal stays refused until the
			-- other person is the one asking.
			status = CASE
				WHEN friendships.requester_id = excluded.requester_id THEN friendships.status
				WHEN friendships.status = 'pending'  THEN 'accepted'
				WHEN friendships.status = 'declined' THEN 'pending'
				ELSE friendships.status END,
			requester_id = CASE
				WHEN friendships.status = 'declined'
				 AND friendships.requester_id <> excluded.requester_id THEN excluded.requester_id
				ELSE friendships.requester_id END,
			decided_at = CASE
				WHEN friendships.status = 'pending'
				 AND friendships.requester_id <> excluded.requester_id THEN now()
				ELSE friendships.decided_at END
		RETURNING status, requester_id, (xmax = 0) AS created
		)
		SELECT upsert.status, upsert.requester_id, upsert.created,
		       (SELECT status FROM before)
		  FROM upsert`,
		requesterID, targetID).Scan(&status, &requester, &created, &before)

	if errors.Is(err, pgx.ErrNoRows) {
		// The insert's WHERE refused it. Which of the three reasons it was
		// decides the answer, and being blocked must not be one the caller can
		// detect -- so that case reports the same success as an ordinary send.
		return f.explainRefusal(ctx, requesterID, targetID)
	}
	if err != nil {
		return RequestOutcome{}, fmt.Errorf("store: sending friend request: %w", err)
	}
	changed := created || before == nil || FriendStatus(*before) != status
	// A refused request reports as sent; only the tombstone knows better.
	if status == FriendDeclined {
		status = FriendPending
	}
	return RequestOutcome{Status: status, Created: created, Changed: changed}, nil
}

// explainRefusal runs only on the path where nothing was written, so the extra
// read costs nobody anything in the normal case.
func (f *Friends) explainRefusal(ctx context.Context, requesterID, targetID string) (RequestOutcome, error) {
	var callerOK, targetOK bool
	err := f.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM users WHERE id = $1::uuid AND NOT is_guest AND deleted_at IS NULL),
		       EXISTS (SELECT 1 FROM users WHERE id = $2::uuid AND NOT is_guest AND deleted_at IS NULL)`,
		requesterID, targetID).Scan(&callerOK, &targetOK)
	if err != nil {
		return RequestOutcome{}, fmt.Errorf("store: sending friend request: %w", err)
	}
	switch {
	case !callerOK:
		return RequestOutcome{}, ErrGuestAccount
	case !targetOK:
		return RequestOutcome{}, ErrNotFriendable
	default:
		// Only a block is left. The request is dropped without an error and
		// without a push: the blocker hears nothing. It is not perfectly hidden
		// -- an ordinary request leaves a row in the sender's own list and this
		// one does not -- but nothing is written and nobody is disturbed, which
		// is what blocking is for (docs/08 §4.2).
		return RequestOutcome{Status: FriendPending, Created: true, Changed: false}, nil
	}
}

// Accept answers a request waiting from `otherID`. Blocks are re-checked here
// because one may have been made while the request sat in the inbox.
func (f *Friends) Accept(ctx context.Context, userID, otherID string) error {
	return f.decide(ctx, userID, otherID, FriendAccepted)
}

// Decline refuses it. The row stays: that is what stops the same person asking
// again, and only their asking again would be stopped -- the person who declined
// may still send their own request later, which revives the row.
func (f *Friends) Decline(ctx context.Context, userID, otherID string) error {
	return f.decide(ctx, userID, otherID, FriendDeclined)
}

func (f *Friends) decide(ctx context.Context, userID, otherID string, status FriendStatus) error {
	// Accepting makes a friendship, so both sides must still be eligible -- an
	// Apple ID revoked while the request waited must not become one by being
	// answered. Declining stays open to anyone, or a demoted account could never
	// clear its inbox.
	eligible := ""
	if status == FriendAccepted {
		eligible = `
		   AND EXISTS (SELECT 1 FROM users WHERE id = $1::uuid AND NOT is_guest AND deleted_at IS NULL)
		   AND EXISTS (SELECT 1 FROM users WHERE id = $2::uuid AND NOT is_guest AND deleted_at IS NULL)`
	}
	tag, err := f.pool.Exec(ctx, `
		UPDATE friendships SET status = $3, decided_at = now()
		 WHERE user_low = least($1::uuid, $2::uuid)
		   AND user_high = greatest($1::uuid, $2::uuid)
		   AND status = 'pending'
		   AND requester_id = $2::uuid
		   AND `+blockedPair+eligible, userID, otherID, string(status))
	if err != nil {
		return fmt.Errorf("store: answering friend request: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Remove unfriends, or takes back a request we sent. A refusal is not ours to
// delete: letting the refused party clear it would hand back the resend they
// were just refused.
func (f *Friends) Remove(ctx context.Context, userID, otherID string) error {
	tag, err := f.pool.Exec(ctx, `
		DELETE FROM friendships
		 WHERE user_low = least($1::uuid, $2::uuid)
		   AND user_high = greatest($1::uuid, $2::uuid)
		   AND (status = 'accepted' OR (status = 'pending' AND requester_id = $1::uuid))`,
		userID, otherID)
	if err != nil {
		return fmt.Errorf("store: removing friend: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns everyone connected to this account: accepted friends, requests
// waiting for an answer, and requests sent. Declined rows are nobody's business
// and never appear. A deleted account drops out of the list.
func (f *Friends) List(ctx context.Context, userID string) ([]Friend, error) {
	rows, err := f.pool.Query(ctx, `
		SELECT u.id, u.display_name, u.friend_code, fr.status,
		       fr.requester_id <> $1::uuid AS incoming, fr.created_at
		  FROM friendships fr
		  JOIN users u ON u.id = CASE WHEN fr.user_low = $1::uuid THEN fr.user_high ELSE fr.user_low END
		 WHERE (fr.user_low = $1::uuid OR fr.user_high = $1::uuid)
		   AND fr.status <> 'declined'
		   AND u.deleted_at IS NULL
		   -- A block and a request can commit in either order; the block wins
		   -- here whichever way that race went.
		   AND NOT EXISTS (SELECT 1 FROM blocks
		        WHERE (blocker_id = $1::uuid AND blocked_id = u.id)
		           OR (blocker_id = u.id AND blocked_id = $1::uuid))
		 ORDER BY fr.status, fr.created_at DESC
		 LIMIT 500`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: listing friends: %w", err)
	}
	defer rows.Close()

	out := []Friend{}
	for rows.Next() {
		var friend Friend
		if err := rows.Scan(&friend.UserID, &friend.DisplayName, &friend.FriendCode,
			&friend.Status, &friend.Incoming, &friend.CreatedAt); err != nil {
			return nil, err
		}
		// "Waiting for them" is not incoming; only a pending row can be either.
		if friend.Status != FriendPending {
			friend.Incoming = false
		}
		out = append(out, friend)
	}
	return out, rows.Err()
}

// ByCode resolves a friend code to the account behind it. A code belonging to a
// guest, a deleted account, or someone either side has blocked answers exactly
// like a code nobody owns -- same error, same work done (docs/08 §4.2).
func (f *Friends) ByCode(ctx context.Context, viewerID, code string) (User, error) {
	var user User
	err := f.pool.QueryRow(ctx, `
		SELECT u.id, u.display_name, u.friend_code, u.is_guest
		  FROM users u
		 WHERE u.friend_code = $2
		   AND u.deleted_at IS NULL
		   AND NOT u.is_guest
		   AND u.id <> $1::uuid
		   AND NOT EXISTS (SELECT 1 FROM blocks
		        WHERE (blocker_id = $1::uuid AND blocked_id = u.id)
		           OR (blocker_id = u.id AND blocked_id = $1::uuid))`,
		viewerID, code).Scan(&user.ID, &user.DisplayName, &user.FriendCode, &user.IsGuest)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("store: looking up friend code: %w", err)
	}
	return user, nil
}
