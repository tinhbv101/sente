package store

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Users struct{ pool *pgxpool.Pool }

func NewUsers(pool *pgxpool.Pool) *Users { return &Users{pool: pool} }

type User struct {
	ID          string
	DisplayName string
	FriendCode  string
	IsGuest     bool
}

// Display names are generated so a player can start without choosing one
// (docs/01 FR-A1). Deliberately playful and clearly temporary, so people rename
// themselves rather than assuming the name is theirs forever.
var (
	nameAdjectives = []string{"an", "binh", "chi", "dung", "giang", "ha", "khanh",
		"lam", "minh", "nam", "phuc", "quan", "son", "thao", "vinh"}
	nameNouns = []string{"co-vay", "thien-nguyen", "kiep", "dat", "khi", "sao", "mat"}
)

func generateDisplayName() string {
	return fmt.Sprintf("%s-%s-%d",
		nameAdjectives[rand.Intn(len(nameAdjectives))],
		nameNouns[rand.Intn(len(nameNouns))],
		rand.Intn(9000)+1000)
}

// CreateGuest makes an account with no sign-in attached. Retries on the vanishing
// chance of a friend-code collision: the database constraint is what actually
// guarantees uniqueness, so the code has to cope with losing that race.
func (u *Users) CreateGuest(ctx context.Context) (User, error) {
	for attempt := 0; attempt < 5; attempt++ {
		user := User{
			ID:          NewID(),
			DisplayName: generateDisplayName(),
			FriendCode:  NewFriendCode(),
			IsGuest:     true,
		}
		_, err := u.pool.Exec(ctx, `
			INSERT INTO users (id, display_name, friend_code, is_guest)
			VALUES ($1, $2, $3, TRUE)`, user.ID, user.DisplayName, user.FriendCode)
		if err == nil {
			return user, nil
		}
		if !strings.Contains(err.Error(), "users_friend_code_key") {
			return User{}, fmt.Errorf("store: creating guest: %w", err)
		}
	}
	return User{}, errors.New("store: could not find a free friend code")
}

func (u *Users) Get(ctx context.Context, id string) (User, error) {
	var user User
	err := u.pool.QueryRow(ctx, `
		SELECT id, display_name, friend_code, is_guest
		  FROM users WHERE id = $1 AND deleted_at IS NULL`, id).
		Scan(&user.ID, &user.DisplayName, &user.FriendCode, &user.IsGuest)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("store: loading user: %w", err)
	}
	return user, nil
}
