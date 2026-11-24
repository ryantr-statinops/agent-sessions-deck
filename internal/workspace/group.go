package workspace

import (
	"time"
)

// SessionRecord holds the session metadata necessary for workspace grouping.
type SessionRecord struct {
	ID         string
	Workspace  ID
	Active     bool
	ObservedAt time.Time
}

// GroupByWorkspace groups session records by their workspace ID.
func GroupByWorkspace(sessions []SessionRecord) map[ID][]SessionRecord {
	groups := make(map[ID][]SessionRecord)
	for _, s := range sessions {
		groups[s.Workspace] = append(groups[s.Workspace], s)
	}
	return groups
}

// SummarizeGroup builds a WorkspaceSummary from a Workspace and its constituent session records.
func SummarizeGroup(ws Workspace, sessions []SessionRecord) WorkspaceSummary {
	summary := WorkspaceSummary{
		Workspace:  ws,
		TotalCount: len(sessions),
		SessionIDs: make([]string, 0, len(sessions)),
	}
	if g, ok := ws.Git(); ok {
		summary.Git = &g
	}
	for _, s := range sessions {
		summary.SessionIDs = append(summary.SessionIDs, s.ID)
		if s.Active {
			summary.ActiveCount++
		}
		if s.ObservedAt.After(summary.LastObservedAt) {
			summary.LastObservedAt = s.ObservedAt
		}
	}
	return summary
}
