package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestGitThrottleCache(t *testing.T) {
	currTime := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return currTime }

	probeCount := 0
	prober := func(ctx context.Context, dir string) (workspace.Git, error) {
		probeCount++
		return workspace.Git{
			Root:   dir,
			Branch: "main",
			Dirty:  probeCount > 1,
		}, nil
	}

	cache := NewGitThrottleCache(10*time.Second, prober, clock)
	ctx := context.Background()
	dir := "/workspace/test"

	// 1. Initial probe
	g1, err := cache.Probe(ctx, dir)
	if err != nil {
		t.Fatalf("unexpected probe error: %v", err)
	}
	if probeCount != 1 {
		t.Fatalf("expected 1 probe call, got %d", probeCount)
	}
	if g1.Dirty {
		t.Fatalf("expected clean in first probe")
	}

	// 2. Cache hit at +5s
	currTime = currTime.Add(5 * time.Second)
	g2, err := cache.Probe(ctx, dir)
	if err != nil {
		t.Fatalf("unexpected probe error: %v", err)
	}
	if probeCount != 1 {
		t.Fatalf("expected cache hit (probe count 1), got %d", probeCount)
	}
	if g2.Dirty {
		t.Fatalf("expected cached clean result")
	}

	// 3. Cache expiration at +11s
	currTime = currTime.Add(6 * time.Second)
	g3, err := cache.Probe(ctx, dir)
	if err != nil {
		t.Fatalf("unexpected probe error: %v", err)
	}
	if probeCount != 2 {
		t.Fatalf("expected cache miss on expiry (probe count 2), got %d", probeCount)
	}
	if !g3.Dirty {
		t.Fatalf("expected refreshed dirty result")
	}

	// 4. Invalidation
	cache.Invalidate(dir)
	_, _ = cache.Probe(ctx, dir)
	if probeCount != 3 {
		t.Fatalf("expected probe count 3 after invalidation, got %d", probeCount)
	}
}

func TestGitThrottleCacheErrorCaching(t *testing.T) {
	currTime := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return currTime }

	calls := 0
	expectedErr := errors.New("not a git repo")
	prober := func(ctx context.Context, dir string) (workspace.Git, error) {
		calls++
		return workspace.Git{}, expectedErr
	}

	cache := NewGitThrottleCache(10*time.Second, prober, clock)
	ctx := context.Background()

	_, err1 := cache.Probe(ctx, "/not/repo")
	if !errors.Is(err1, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err1)
	}

	// Within TTL, error is returned from cache without extra probe
	currTime = currTime.Add(2 * time.Second)
	_, err2 := cache.Probe(ctx, "/not/repo")
	if !errors.Is(err2, expectedErr) {
		t.Fatalf("expected cached error %v, got %v", expectedErr, err2)
	}
	if calls != 1 {
		t.Fatalf("expected calls to be 1, got %d", calls)
	}
}

func TestGitThrottleCacheConcurrent(t *testing.T) {
	currTime := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return currTime }

	prober := func(ctx context.Context, dir string) (workspace.Git, error) {
		return workspace.Git{Root: dir, Branch: "dev"}, nil
	}

	cache := NewGitThrottleCache(10*time.Second, prober, clock)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, err := cache.Probe(ctx, "/workspace/concurrent")
			if err != nil || g.Branch != "dev" {
				t.Errorf("unexpected probe result: %v, %v", g, err)
			}
		}()
	}
	wg.Wait()
}
