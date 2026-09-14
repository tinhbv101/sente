// Package ratelimit throttles callers using a token bucket held in Redis.
//
// Shared state, not per-process counters: with several nodes behind one load
// balancer, a limit each node enforces on its own is really the limit multiplied
// by the number of nodes.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Rule is a bucket of Capacity requests that refills completely over Window.
//
// A bucket rather than a fixed window: a fixed window lets someone spend a whole
// minute's allowance in the last second and another straight after, which is
// twice the intended rate at exactly the worst moment.
type Rule struct {
	Name     string
	Capacity int
	Window   time.Duration
}

func (r Rule) refillPerMillisecond() float64 {
	return float64(r.Capacity) / float64(r.Window.Milliseconds())
}

type Result struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
	ResetAt    time.Time
}

type Limiter struct {
	client *redis.Client
	now    func() time.Time
	prefix string
}

func New(client *redis.Client) *Limiter {
	return &Limiter{client: client, now: time.Now}
}

// WithPrefix namespaces every key. Several limiters sharing one Redis -- test
// servers, or a staging and a production node on one instance -- stay apart.
func (l *Limiter) WithPrefix(prefix string) *Limiter {
	copied := *l
	copied.prefix = prefix
	return &copied
}

// The whole decision happens inside Redis. Read-then-write from the application
// would let two concurrent requests both see the last token.
var script = redis.NewScript(`
	local capacity   = tonumber(ARGV[1])
	local refill     = tonumber(ARGV[2])
	local now        = tonumber(ARGV[3])
	local ttl        = tonumber(ARGV[4])

	local state  = redis.call("HMGET", KEYS[1], "tokens", "ts")
	local tokens = tonumber(state[1])
	local ts     = tonumber(state[2])
	if tokens == nil or ts == nil then
		tokens = capacity
		ts = now
	end

	local elapsed = math.max(0, now - ts)
	tokens = math.min(capacity, tokens + elapsed * refill)

	local allowed = 0
	if tokens >= 1 then
		tokens = tokens - 1
		allowed = 1
	end

	redis.call("HSET", KEYS[1], "tokens", tokens, "ts", now)
	redis.call("PEXPIRE", KEYS[1], ttl)

	-- How long until one whole token is available again, and until the bucket is
	-- full. Both in milliseconds, because Lua returns integers.
	local retry = 0
	if allowed == 0 then
		retry = math.ceil((1 - tokens) / refill)
	end
	local reset = math.ceil((capacity - tokens) / refill)
	return {allowed, math.floor(tokens), retry, reset}
`)

// Allow spends one token for key under rule.
//
// On a Redis failure it returns the error and leaves the decision to the caller.
// Whether to fail open or closed is a policy question, and burying it here would
// make it invisible.
func (l *Limiter) Allow(ctx context.Context, rule Rule, key string) (Result, error) {
	now := l.now().UnixMilli()
	// Keep an idle bucket around for two windows, so a caller who pauses does not
	// come back to a full bucket immediately.
	ttl := rule.Window.Milliseconds() * 2

	raw, err := script.Run(ctx, l.client,
		[]string{"rl:" + l.prefix + rule.Name + ":" + key},
		rule.Capacity, rule.refillPerMillisecond(), now, ttl).Int64Slice()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: %w", err)
	}
	if len(raw) != 4 {
		return Result{}, fmt.Errorf("ratelimit: unexpected reply of length %d", len(raw))
	}
	return Result{
		Allowed:    raw[0] == 1,
		Limit:      rule.Capacity,
		Remaining:  int(raw[1]),
		RetryAfter: time.Duration(raw[2]) * time.Millisecond,
		ResetAt:    l.now().Add(time.Duration(raw[3]) * time.Millisecond),
	}, nil
}

// Rules from docs/06 §1.3. Named so a change here is a change everywhere.
var (
	SignUp       = Rule{Name: "signup", Capacity: 10, Window: time.Minute}
	CreateInvite = Rule{Name: "invite", Capacity: 20, Window: time.Hour}
	AcceptInvite = Rule{Name: "accept", Capacity: 30, Window: time.Hour}
	CreateGame   = Rule{Name: "creategame", Capacity: 30, Window: time.Hour}
	Read         = Rule{Name: "read", Capacity: 120, Window: time.Minute}
	// FriendLookup is the anti-enumeration budget docs/08 §4.2 sets against a
	// 2^40 code space.
	FriendLookup = Rule{Name: "friendlookup", Capacity: 10, Window: time.Minute}
	// FriendWrite is deliberately far tighter than a read: every one of these
	// changes somebody else's account and can ring their phone.
	FriendWrite = Rule{Name: "friendwrite", Capacity: 30, Window: time.Hour}
	// Socket is per connection, not per user: someone with two devices is not
	// abusing anything.
	Socket = Rule{Name: "socket", Capacity: 30, Window: 10 * time.Second}
)
