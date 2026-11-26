package store

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
)

func createBenchmarkJournal(b *testing.B, count int) (*EventJournal, string) {
	b.Helper()
	dir := b.TempDir()
	path := filepath.Join(dir, "bench_events.jsonl")
	lockPath := filepath.Join(dir, ".lock")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		b.Fatalf("create bench journal file: %v", err)
	}

	w := bufio.NewWriterSize(f, 256*1024)
	baseTime := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	for i := 1; i <= count; i++ {
		sessID := "s-000001"
		if i%3 == 0 {
			sessID = "s-000002"
		} else if i%3 == 1 {
			sessID = "s-000003"
		}

		ev := events.NewEvent(
			events.ID(fmt.Sprintf("evt-%08d", i)),
			events.TypeSessionStarted,
			events.SessionID(sessID),
			events.AttemptGeneration(1),
			events.Revision(i),
			events.NewTimestamp(baseTime.Add(time.Duration(i)*time.Millisecond)),
			events.Metadata{},
		)

		data, err := json.Marshal(ev)
		if err != nil {
			b.Fatalf("marshal bench event: %v", err)
		}
		if _, err := w.Write(data); err != nil {
			b.Fatalf("write bench event: %v", err)
		}
		if err := w.WriteByte('\n'); err != nil {
			b.Fatalf("write bench newline: %v", err)
		}
	}

	if err := w.Flush(); err != nil {
		b.Fatalf("flush bench buffer: %v", err)
	}
	if err := f.Sync(); err != nil {
		b.Fatalf("sync bench file: %v", err)
	}
	if err := f.Close(); err != nil {
		b.Fatalf("close bench file: %v", err)
	}

	j, err := NewEventJournal(path, lockPath)
	if err != nil {
		b.Fatalf("NewEventJournal on bench file: %v", err)
	}
	return j, path
}

func BenchmarkEventJournalQueryTail(b *testing.B) {
	const totalEvents = 100000
	j, _ := createBenchmarkJournal(b, totalEvents)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Query the last 50 events using cursor
		results, nextRev, err := j.Query(ctx, EventFilter{
			AfterRevision: totalEvents - 50,
			Limit:         50,
		})
		if err != nil {
			b.Fatalf("Query: %v", err)
		}
		if len(results) != 50 || nextRev != totalEvents {
			b.Fatalf("unexpected results count=%d, nextRev=%d", len(results), nextRev)
		}
	}
}

func BenchmarkEventJournalQueryScan(b *testing.B) {
	const totalEvents = 100000
	j, _ := createBenchmarkJournal(b, totalEvents)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Query first 50 events for a specific session
		results, _, err := j.Query(ctx, EventFilter{
			SessionID: "s-000002",
			Limit:     50,
		})
		if err != nil {
			b.Fatalf("Query: %v", err)
		}
		if len(results) != 50 {
			b.Fatalf("unexpected results count=%d", len(results))
		}
	}
}
