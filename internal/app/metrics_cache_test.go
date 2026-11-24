package app

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/metrics"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestMetricsCacheHitAndExpiry(t *testing.T) {
	currTime := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return currTime }

	sampleCount := 0
	sampler := func(identity session.ProcessIdentity, attemptStartedAt time.Time, prev *metrics.SampleRecord, now time.Time) (metrics.ProcessMetrics, metrics.SampleRecord, error) {
		sampleCount++
		return metrics.ProcessMetrics{
			PID:            identity.PID,
			CPUPercent:     float64(sampleCount * 10),
			MemoryRSSBytes: 1024 * 1024,
			ProcessCount:   1,
			SampledAt:      now,
		}, metrics.SampleRecord{SampledAt: now, TotalTicks: uint64(sampleCount * 100)}, nil
	}

	cache := NewMetricsCache(2*time.Second, sampler, clock)
	sid := session.ID("test-sess-1")
	ident := session.ProcessIdentity{PID: 1234, StartTicks: 500}

	// 1. Initial sample
	m1 := cache.GetOrSample(sid, ident, currTime)
	if sampleCount != 1 {
		t.Fatalf("expected 1 sample, got %d", sampleCount)
	}
	if m1.CPUPercent != 10.0 {
		t.Fatalf("expected CPU 10.0, got %f", m1.CPUPercent)
	}

	// 2. Cache hit at +1s
	currTime = currTime.Add(1 * time.Second)
	m2 := cache.GetOrSample(sid, ident, currTime)
	if sampleCount != 1 {
		t.Fatalf("expected cache hit (sample count 1), got %d", sampleCount)
	}
	if m2.CPUPercent != 10.0 {
		t.Fatalf("expected cached CPU 10.0, got %f", m2.CPUPercent)
	}

	// 3. Expiry at +3s
	currTime = currTime.Add(2 * time.Second)
	m3 := cache.GetOrSample(sid, ident, currTime)
	if sampleCount != 2 {
		t.Fatalf("expected cache miss on expiry (sample count 2), got %d", sampleCount)
	}
	if m3.CPUPercent != 20.0 {
		t.Fatalf("expected CPU 20.0 after re-sample, got %f", m3.CPUPercent)
	}
}

func TestMetricsCacheStaleFallback(t *testing.T) {
	currTime := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return currTime }

	failSample := false
	sampler := func(identity session.ProcessIdentity, attemptStartedAt time.Time, prev *metrics.SampleRecord, now time.Time) (metrics.ProcessMetrics, metrics.SampleRecord, error) {
		if failSample {
			return metrics.ProcessMetrics{}, metrics.SampleRecord{}, errors.New("procfs read error")
		}
		return metrics.ProcessMetrics{
			PID:            identity.PID,
			CPUPercent:     15.0,
			MemoryRSSBytes: 5000,
			SampledAt:      now,
		}, metrics.SampleRecord{SampledAt: now, TotalTicks: 100}, nil
	}

	cache := NewMetricsCache(2*time.Second, sampler, clock)
	sid := session.ID("test-sess-2")
	ident := session.ProcessIdentity{PID: 2222, StartTicks: 800}

	// Initial sample succeeds
	m1 := cache.GetOrSample(sid, ident, currTime)
	if m1.Stale || m1.CPUPercent != 15.0 {
		t.Fatalf("unexpected initial metric: %+v", m1)
	}

	// Advance time past TTL and fail sample
	currTime = currTime.Add(3 * time.Second)
	failSample = true

	m2 := cache.GetOrSample(sid, ident, currTime)
	if !m2.Stale {
		t.Fatalf("expected Stale=true fallback on sample error")
	}
	if m2.CPUPercent != 15.0 {
		t.Fatalf("expected cached CPU 15.0 on fallback, got %f", m2.CPUPercent)
	}
}

func TestMetricsCachePIDChangeAndInvalidate(t *testing.T) {
	currTime := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return currTime }

	var lastPrev *metrics.SampleRecord
	sampler := func(identity session.ProcessIdentity, attemptStartedAt time.Time, prev *metrics.SampleRecord, now time.Time) (metrics.ProcessMetrics, metrics.SampleRecord, error) {
		lastPrev = prev
		return metrics.ProcessMetrics{
			PID:       identity.PID,
			SampledAt: now,
		}, metrics.SampleRecord{SampledAt: now, TotalTicks: 100}, nil
	}

	cache := NewMetricsCache(2*time.Second, sampler, clock)
	sid := session.ID("test-sess-3")
	ident1 := session.ProcessIdentity{PID: 1001, StartTicks: 100}

	cache.GetOrSample(sid, ident1, currTime)
	if lastPrev != nil {
		t.Fatalf("expected nil prev for new session")
	}

	// Expiry with same PID -> prev is populated
	currTime = currTime.Add(3 * time.Second)
	cache.GetOrSample(sid, ident1, currTime)
	if lastPrev == nil {
		t.Fatalf("expected non-nil prev for same PID")
	}

	// New PID for same session -> prev must be nil
	ident2 := session.ProcessIdentity{PID: 2002, StartTicks: 200}
	cache.GetOrSample(sid, ident2, currTime)
	if lastPrev != nil {
		t.Fatalf("expected nil prev for changed PID")
	}

	// Invalidate
	cache.Invalidate(sid)
	cache.mu.RLock()
	_, exists := cache.entries[sid]
	cache.mu.RUnlock()
	if exists {
		t.Fatalf("expected cache entry to be removed after Invalidate")
	}
}

func TestMetricsCacheConcurrent(t *testing.T) {
	currTime := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return currTime }

	sampler := func(identity session.ProcessIdentity, attemptStartedAt time.Time, prev *metrics.SampleRecord, now time.Time) (metrics.ProcessMetrics, metrics.SampleRecord, error) {
		return metrics.ProcessMetrics{
			PID:       identity.PID,
			SampledAt: now,
		}, metrics.SampleRecord{SampledAt: now, TotalTicks: 100}, nil
	}

	cache := NewMetricsCache(2*time.Second, sampler, clock)
	sid := session.ID("test-sess-concurrent")
	ident := session.ProcessIdentity{PID: 5555, StartTicks: 10}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := cache.GetOrSample(sid, ident, currTime)
			if m.PID != 5555 {
				t.Errorf("expected PID 5555, got %d", m.PID)
			}
		}()
	}
	wg.Wait()
}
