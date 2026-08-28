package ratelimit

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// Against a real Redis: the arithmetic lives in a Lua script, and the property
// that matters -- that concurrent callers cannot both spend the last token -- only
// exists because Redis runs the script atomically.

var testRedis *redis.Client

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	ctx := context.Background()
	container, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start redis (is Docker running?): %v\n", err)
		os.Exit(1)
	}
	uri, _ := container.ConnectionString(ctx)
	options, _ := redis.ParseURL(uri)
	testRedis = redis.NewClient(options)

	code := m.Run()
	_ = testRedis.Close()
	_ = testcontainers.TerminateContainer(container)
	os.Exit(code)
}

func newLimiter(t *testing.T) *Limiter {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	return New(testRedis)
}

func TestABucketAllowsItsCapacityThenRefuses(t *testing.T) {
	limiter := newLimiter(t)
	ctx := context.Background()
	rule := Rule{Name: t.Name(), Capacity: 5, Window: time.Minute}

	for i := 1; i <= 5; i++ {
		result, err := limiter.Allow(ctx, rule, "caller")
		if err != nil {
			t.Fatal(err)
		}
		if !result.Allowed {
			t.Fatalf("request %d should have been allowed", i)
		}
		if result.Remaining != 5-i {
			t.Errorf("request %d: want %d remaining, got %d", i, 5-i, result.Remaining)
		}
	}
	result, err := limiter.Allow(ctx, rule, "caller")
	if err != nil {
		t.Fatal(err)
	}
	if result.Allowed {
		t.Fatal("the sixth request should have been refused")
	}
	if result.RetryAfter <= 0 {
		t.Error("a refused caller must be told when to come back")
	}
	if result.Limit != 5 {
		t.Errorf("the limit should be reported, got %d", result.Limit)
	}
}

func TestCallersAreCountedSeparately(t *testing.T) {
	limiter := newLimiter(t)
	ctx := context.Background()
	rule := Rule{Name: t.Name(), Capacity: 2, Window: time.Minute}

	for i := 0; i < 2; i++ {
		if result, _ := limiter.Allow(ctx, rule, "first"); !result.Allowed {
			t.Fatal("the first caller should still have room")
		}
	}
	if result, _ := limiter.Allow(ctx, rule, "first"); result.Allowed {
		t.Fatal("the first caller is out")
	}
	// One caller running out must not affect anybody else.
	if result, _ := limiter.Allow(ctx, rule, "second"); !result.Allowed {
		t.Error("a second caller has their own bucket")
	}
}

func TestRulesAreCountedSeparately(t *testing.T) {
	limiter := newLimiter(t)
	ctx := context.Background()
	cheap := Rule{Name: t.Name() + "-cheap", Capacity: 1, Window: time.Minute}
	other := Rule{Name: t.Name() + "-other", Capacity: 1, Window: time.Minute}

	if result, _ := limiter.Allow(ctx, cheap, "caller"); !result.Allowed {
		t.Fatal("setup")
	}
	if result, _ := limiter.Allow(ctx, cheap, "caller"); result.Allowed {
		t.Fatal("that rule is exhausted")
	}
	if result, _ := limiter.Allow(ctx, other, "caller"); !result.Allowed {
		t.Error("a different rule has its own bucket")
	}
}

// The bucket refills continuously, so waiting a fraction of the window buys back
// a fraction of the allowance -- not nothing, and not the whole thing.
func TestTheBucketRefillsGradually(t *testing.T) {
	limiter := newLimiter(t)
	ctx := context.Background()
	rule := Rule{Name: t.Name(), Capacity: 10, Window: time.Second}

	// Drive the clock rather than sleeping.
	base := time.Now()
	limiter.now = func() time.Time { return base }
	for i := 0; i < 10; i++ {
		if result, _ := limiter.Allow(ctx, rule, "caller"); !result.Allowed {
			t.Fatalf("request %d should have been allowed", i)
		}
	}
	if result, _ := limiter.Allow(ctx, rule, "caller"); result.Allowed {
		t.Fatal("the bucket should be empty")
	}

	// A third of the window back is a third of the allowance.
	limiter.now = func() time.Time { return base.Add(333 * time.Millisecond) }
	allowed := 0
	for i := 0; i < 10; i++ {
		if result, _ := limiter.Allow(ctx, rule, "caller"); result.Allowed {
			allowed++
		}
	}
	if allowed < 2 || allowed > 4 {
		t.Errorf("want roughly three requests back, got %d", allowed)
	}

	// A full window later the bucket is full again, and no more than full.
	limiter.now = func() time.Time { return base.Add(10 * time.Second) }
	allowed = 0
	for i := 0; i < 20; i++ {
		if result, _ := limiter.Allow(ctx, rule, "caller"); result.Allowed {
			allowed++
		}
	}
	if allowed != 10 {
		t.Errorf("a bucket must never hold more than its capacity, got %d", allowed)
	}
}

// The reason the decision lives in a Lua script: without atomicity two callers
// racing on the last token both see it.
func TestConcurrentCallersCannotOverspend(t *testing.T) {
	limiter := newLimiter(t)
	ctx := context.Background()
	rule := Rule{Name: t.Name(), Capacity: 20, Window: time.Hour}

	var mu sync.Mutex
	allowed := 0
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if result, err := limiter.Allow(ctx, rule, "caller"); err == nil && result.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if allowed != 20 {
		t.Errorf("want exactly the capacity allowed, got %d of 100", allowed)
	}
}

func TestRetryAfterIsUsable(t *testing.T) {
	limiter := newLimiter(t)
	ctx := context.Background()
	rule := Rule{Name: t.Name(), Capacity: 1, Window: 10 * time.Second}

	base := time.Now()
	limiter.now = func() time.Time { return base }
	if result, _ := limiter.Allow(ctx, rule, "caller"); !result.Allowed {
		t.Fatal("setup")
	}
	refused, _ := limiter.Allow(ctx, rule, "caller")
	if refused.Allowed {
		t.Fatal("the bucket should be empty")
	}
	// One token in a ten-second window takes ten seconds to come back.
	if refused.RetryAfter < 9*time.Second || refused.RetryAfter > 11*time.Second {
		t.Errorf("retry-after should be about ten seconds, got %v", refused.RetryAfter)
	}
	// Waiting exactly that long must actually work, or the header is a lie.
	limiter.now = func() time.Time { return base.Add(refused.RetryAfter) }
	if result, _ := limiter.Allow(ctx, rule, "caller"); !result.Allowed {
		t.Error("the caller waited as told and was still refused")
	}
}

func TestRedisFailuresAreReportedNotSwallowed(t *testing.T) {
	limiter := newLimiter(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	// Whether to fail open or closed is the caller's policy, so the error has to
	// reach them rather than being turned into a silent "allowed".
	if _, err := limiter.Allow(cancelled, Read, "caller"); err == nil {
		t.Error("a Redis failure must be reported")
	}
}

func TestTheConfiguredRulesAreSane(t *testing.T) {
	for _, rule := range []Rule{SignUp, CreateInvite, AcceptInvite, CreateGame, Read, Socket} {
		if rule.Name == "" || rule.Capacity <= 0 || rule.Window <= 0 {
			t.Errorf("incomplete rule: %+v", rule)
		}
		if rule.refillPerMillisecond() <= 0 {
			t.Errorf("%s refills at zero, so a spent bucket never recovers", rule.Name)
		}
	}
	if SignUp.Capacity > Read.Capacity {
		t.Error("signing up should be tighter than reading")
	}
}
