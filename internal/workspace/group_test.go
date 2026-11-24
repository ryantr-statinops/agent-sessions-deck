package workspace

import (
	"testing"
	"time"
)

func TestGroupByWorkspaceAndSummarize(t *testing.T) {
	ws1, err := New("/home/alice/repo-a")
	if err != nil {
		t.Fatalf("create ws1: %v", err)
	}
	ws1, _ = ws1.WithGit(Git{
		Root:   "/home/alice/repo-a",
		Branch: "feat/stage12",
	})

	ws2, err := New("/home/alice/repo-b")
	if err != nil {
		t.Fatalf("create ws2: %v", err)
	}

	t0 := time.Now().UTC().Add(-10 * time.Minute)
	t1 := time.Now().UTC().Add(-5 * time.Minute)
	t2 := time.Now().UTC()

	sessions := []SessionRecord{
		{ID: "sess-1", Workspace: ws1.ID(), Active: true, ObservedAt: t0},
		{ID: "sess-2", Workspace: ws1.ID(), Active: false, ObservedAt: t1},
		{ID: "sess-3", Workspace: ws2.ID(), Active: true, ObservedAt: t2},
	}

	grouped := GroupByWorkspace(sessions)
	if len(grouped) != 2 {
		t.Fatalf("expected 2 workspace groups, got %d", len(grouped))
	}
	if len(grouped[ws1.ID()]) != 2 {
		t.Fatalf("expected 2 sessions in ws1, got %d", len(grouped[ws1.ID()]))
	}
	if len(grouped[ws2.ID()]) != 1 {
		t.Fatalf("expected 1 session in ws2, got %d", len(grouped[ws2.ID()]))
	}

	sum1 := SummarizeGroup(ws1, grouped[ws1.ID()])
	if sum1.ActiveCount != 1 || sum1.TotalCount != 2 {
		t.Fatalf("expected 1 active / 2 total in ws1, got %d / %d", sum1.ActiveCount, sum1.TotalCount)
	}
	if !sum1.LastObservedAt.Equal(t1) {
		t.Fatalf("expected last observed at %v, got %v", t1, sum1.LastObservedAt)
	}
	if sum1.Git == nil || sum1.Git.Branch != "feat/stage12" {
		t.Fatalf("expected git branch feat/stage12, got: %+v", sum1.Git)
	}
	if err := sum1.Validate(); err != nil {
		t.Fatalf("summary validation failed: %v", err)
	}

	sum2 := SummarizeGroup(ws2, grouped[ws2.ID()])
	if sum2.ActiveCount != 1 || sum2.TotalCount != 1 {
		t.Fatalf("expected 1 active / 1 total in ws2, got %d / %d", sum2.ActiveCount, sum2.TotalCount)
	}
	if sum2.Git != nil {
		t.Fatalf("expected no git in ws2, got: %+v", sum2.Git)
	}
	if err := sum2.Validate(); err != nil {
		t.Fatalf("summary validation failed: %v", err)
	}
}
