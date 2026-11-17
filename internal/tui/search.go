package tui

import (
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// FuzzyScore calculates a deterministic match score of pattern against target.
// Returns (score, matched). A higher score indicates a better match.
func FuzzyScore(pattern, target string) (int, bool) {
	if pattern == "" {
		return 0, true
	}
	if target == "" {
		return 0, false
	}

	pLower := strings.ToLower(pattern)
	tLower := strings.ToLower(target)

	// Exact match bonus
	if tLower == pLower {
		return 1000, true
	}

	// Exact prefix match bonus
	if strings.HasPrefix(tLower, pLower) {
		return 600 + (len(pattern) * 10), true
	}

	// Substring match bonus
	if idx := strings.Index(tLower, pLower); idx >= 0 {
		// Bonus if it starts at a word boundary
		bonus := 0
		if idx == 0 || isSeparator(rune(tLower[idx-1])) {
			bonus = 100
		}
		return 400 + bonus - idx + (len(pattern) * 10), true
	}

	// Subsequence fuzzy match
	pRunes := []rune(pLower)
	tRunes := []rune(tLower)

	score := 0
	pIdx := 0
	prevMatchedIdx := -2

	for tIdx := 0; tIdx < len(tRunes) && pIdx < len(pRunes); tIdx++ {
		if tRunes[tIdx] == pRunes[pIdx] {
			score += 10

			// Consecutive character bonus
			if tIdx == prevMatchedIdx+1 {
				score += 20
			}

			// Word boundary bonus
			if tIdx == 0 || isSeparator(tRunes[tIdx-1]) {
				score += 30
			}

			prevMatchedIdx = tIdx
			pIdx++
		}
	}

	// All characters in pattern must have matched in order
	if pIdx == len(pRunes) {
		return score, true
	}

	return 0, false
}

func isSeparator(r rune) bool {
	return r == '/' || r == '-' || r == '_' || r == '.' || r == ' ' || unicode.IsSpace(r)
}

// ScoredSession pairs a session snapshot with its computed search match score.
type ScoredSession struct {
	Session session.SessionSnapshot
	Score   int
}

// FilterSessions filters and deterministically ranks session snapshots.
func FilterSessions(sessions []session.SessionSnapshot, query, agentFilter, statusFilter string) []session.SessionSnapshot {
	if len(sessions) == 0 {
		return nil
	}

	qTrimmed := strings.TrimSpace(query)
	aTrimmed := strings.TrimSpace(agentFilter)
	sTrimmed := strings.TrimSpace(statusFilter)

	// Quick path if no filters or query
	if qTrimmed == "" && aTrimmed == "" && sTrimmed == "" {
		res := make([]session.SessionSnapshot, len(sessions))
		copy(res, sessions)
		// Stable default sort: newest observed first, then ID
		sort.SliceStable(res, func(i, j int) bool {
			if !res[i].ObservedAt.Equal(res[j].ObservedAt) {
				return res[i].ObservedAt.After(res[j].ObservedAt)
			}
			return res[i].ID < res[j].ID
		})
		return res
	}

	var scored []ScoredSession

	for _, s := range sessions {
		// Facet filter: Agent
		if aTrimmed != "" && !strings.EqualFold(string(s.AgentID), aTrimmed) {
			continue
		}

		// Facet filter: Status / Lifecycle
		if sTrimmed != "" && !strings.EqualFold(string(s.Lifecycle), sTrimmed) {
			continue
		}

		if qTrimmed == "" {
			scored = append(scored, ScoredSession{Session: s, Score: 0})
			continue
		}

		// Calculate scores across multiple fields
		bestFieldScore := 0
		matchedAny := false

		// 1. Session Name (weight 3x)
		if sScore, ok := FuzzyScore(qTrimmed, s.Name); ok {
			weighted := sScore * 3
			if weighted > bestFieldScore {
				bestFieldScore = weighted
			}
			matchedAny = true
		}

		// 2. Agent ID (weight 2x)
		if aScore, ok := FuzzyScore(qTrimmed, string(s.AgentID)); ok {
			weighted := aScore * 2
			if weighted > bestFieldScore {
				bestFieldScore = weighted
			}
			matchedAny = true
		}

		// 3. Workspace ID / Path (weight 2x)
		if wScore, ok := FuzzyScore(qTrimmed, string(s.WorkspaceID)); ok {
			weighted := wScore * 2
			if weighted > bestFieldScore {
				bestFieldScore = weighted
			}
			matchedAny = true
		}

		// 4. Session ID (weight 2x)
		if idScore, ok := FuzzyScore(qTrimmed, string(s.ID)); ok {
			weighted := idScore * 2
			if weighted > bestFieldScore {
				bestFieldScore = weighted
			}
			matchedAny = true
		}

		// 5. Lifecycle Status
		if lScore, ok := FuzzyScore(qTrimmed, string(s.Lifecycle)); ok {
			if lScore > bestFieldScore {
				bestFieldScore = lScore
			}
			matchedAny = true
		}

		// 6. PID if available
		if s.HasIdentity && s.Identity.PID > 0 {
			pidStr := strconv.Itoa(s.Identity.PID)
			if pScore, ok := FuzzyScore(qTrimmed, pidStr); ok {
				if pScore > bestFieldScore {
					bestFieldScore = pScore
				}
				matchedAny = true
			}
		}

		if matchedAny {
			scored = append(scored, ScoredSession{
				Session: s,
				Score:   bestFieldScore,
			})
		}
	}

	// Deterministic sort: Score descending -> ObservedAt descending -> Session ID
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		if !scored[i].Session.ObservedAt.Equal(scored[j].Session.ObservedAt) {
			return scored[i].Session.ObservedAt.After(scored[j].Session.ObservedAt)
		}
		return scored[i].Session.ID < scored[j].Session.ID
	})

	out := make([]session.SessionSnapshot, len(scored))
	for i, sc := range scored {
		out[i] = sc.Session
	}
	return out
}
