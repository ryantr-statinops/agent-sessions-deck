//go:build linux

package tests

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/metrics"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/tui"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestStage12_20SessionConcurrentLoadAndSamplingLatency(t *testing.T) {
	procRoot := t.TempDir()
	now := time.Now().UTC()

	numSessions := 20
	sessions := make([]session.SessionSnapshot, numSessions)
	identities := make([]session.ProcessIdentity, numSessions)

	// Create 20 processes across 5 workspaces
	for i := 0; i < numSessions; i++ {
		pid := 7000 + i
		wsPath := fmt.Sprintf("/workspaces/project-%d", i%5)
		startTicks := uint64(1000 + i*10)

		createProcInRoot(t, procRoot, pid, fmt.Sprintf("worker-%d", i), "S", 1, 10, 5, startTicks, 500)

		identities[i] = session.ProcessIdentity{
			PID:        pid,
			StartTicks: startTicks,
		}

		sessions[i] = session.SessionSnapshot{
			ID:          session.ID(fmt.Sprintf("s-%04d", i)),
			Name:        fmt.Sprintf("worker-session-%d", i),
			AgentID:     agent.ID("opencode"),
			WorkspaceID: workspace.ID(wsPath),
			Lifecycle:   session.LifecycleRunning,
			Authority:   session.AuthorityLive,
			HasIdentity: true,
			Identity:    identities[i],
			ObservedAt:  now,
		}
	}

	// 1. Benchmark concurrent procfs sampling across all 20 sessions
	startSample := time.Now()
	var wg sync.WaitGroup
	sampledMetrics := make([]metrics.ProcessMetrics, numSessions)

	for i := 0; i < numSessions; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			m, _, err := metrics.SampleProcessTree(procRoot, identities[idx], now.Add(-5*time.Second), nil, now)
			if err != nil {
				t.Errorf("sample error for session %d: %v", idx, err)
			}
			sampledMetrics[idx] = m
		}(i)
	}
	wg.Wait()
	sampleDuration := time.Since(startSample)

	// Invariant: sampling 20 concurrent sessions must be < 50ms budget
	if sampleDuration > 50*time.Millisecond {
		t.Fatalf("sampling 20 sessions took %v, exceeded 50ms budget", sampleDuration)
	}

	for i, m := range sampledMetrics {
		if m.Unavailable || m.PID != identities[i].PID {
			t.Fatalf("metric mismatch for session %d: %+v", i, m)
		}
	}

	// 2. Benchmark MetricsCache hit speed (sub-millisecond)
	sampler := func(ident session.ProcessIdentity, startedAt time.Time, prev *metrics.SampleRecord, now time.Time) (metrics.ProcessMetrics, metrics.SampleRecord, error) {
		return metrics.ProcessMetrics{PID: ident.PID}, metrics.SampleRecord{}, nil
	}
	cache := app.NewMetricsCache(2*time.Second, sampler, func() time.Time { return now })
	for i := 0; i < numSessions; i++ {
		cache.GetOrSample(sessions[i].ID, identities[i], now)
	}

	startCacheHit := time.Now()
	for i := 0; i < numSessions; i++ {
		m := cache.GetOrSample(sessions[i].ID, identities[i], now)
		if m.PID != identities[i].PID {
			t.Fatalf("cache lookup mismatch")
		}
	}
	cacheHitDuration := time.Since(startCacheHit)
	if cacheHitDuration > 5*time.Millisecond {
		t.Fatalf("cache lookup took %v, want < 5ms", cacheHitDuration)
	}

	// 3. Benchmark TUI workspace-grouped rendering with 20 sessions
	model := tui.NewModel(tui.Options{})
	model.SetWindowSize(120, 40)
	model.SetSessions(sessions)
	model.SetGroupByWorkspace(true)

	startRender := time.Now()
	rendered := model.View()
	renderDuration := time.Since(startRender)

	// Invariant: UI rendering under 20-session load must remain responsive (< 100ms)
	if renderDuration > 100*time.Millisecond {
		t.Fatalf("TUI rendering took %v, exceeded 100ms budget", renderDuration)
	}
	if len(rendered) == 0 {
		t.Fatal("empty rendered output")
	}

	// 4. Memory allocation bounds
	runtime.GC()
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	heapMB := float64(memStats.HeapAlloc) / (1024 * 1024)
	if heapMB > 50.0 {
		t.Fatalf("heap allocation %.2f MB exceeded 50MB budget", heapMB)
	}
}

func BenchmarkSampling20Sessions(b *testing.B) {
	procRoot := b.TempDir()
	now := time.Now().UTC()
	numSessions := 20
	identities := make([]session.ProcessIdentity, numSessions)

	for i := 0; i < numSessions; i++ {
		pid := 8000 + i
		startTicks := uint64(1000 + i*10)
		createProcInRoot(nil, procRoot, pid, fmt.Sprintf("bench-%d", i), "S", 1, 10, 5, startTicks, 200)
		identities[i] = session.ProcessIdentity{PID: pid, StartTicks: startTicks}
	}

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		var wg sync.WaitGroup
		for i := 0; i < numSessions; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				_, _, _ = metrics.SampleProcessTree(procRoot, identities[idx], now.Add(-5*time.Second), nil, now)
			}(i)
		}
		wg.Wait()
	}
}
