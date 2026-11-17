package tui

import (
	"strings"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// FilterSessions filters a slice of SessionSnapshot items by text query and filters.
func FilterSessions(sessions []session.SessionSnapshot, query, agentFilter, statusFilter string) []session.SessionSnapshot {
	if query == "" && agentFilter == "" && statusFilter == "" {
		res := make([]session.SessionSnapshot, len(sessions))
		copy(res, sessions)
		return res
	}

	queryLower := strings.ToLower(strings.TrimSpace(query))
	agentFilterLower := strings.ToLower(strings.TrimSpace(agentFilter))
	statusFilterLower := strings.ToLower(strings.TrimSpace(statusFilter))

	var out []session.SessionSnapshot
	for _, s := range sessions {
		if agentFilterLower != "" && strings.ToLower(string(s.AgentID)) != agentFilterLower {
			continue
		}
		if statusFilterLower != "" && strings.ToLower(string(s.Lifecycle)) != statusFilterLower {
			continue
		}
		if queryLower != "" {
			nameLower := strings.ToLower(s.Name)
			agentLower := strings.ToLower(string(s.AgentID))
			wsLower := strings.ToLower(string(s.WorkspaceID))
			statusStr := strings.ToLower(string(s.Lifecycle))

			matched := strings.Contains(nameLower, queryLower) ||
				strings.Contains(agentLower, queryLower) ||
				strings.Contains(wsLower, queryLower) ||
				strings.Contains(statusStr, queryLower) ||
				strings.Contains(string(s.ID), queryLower)

			if !matched {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}
