//go:build linux

package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/store"
	"github.com/spf13/cobra"
)

func historyCommand(opts Options) *cobra.Command {
	var jsonOutput bool
	var agentID, workspaceID, status, since string
	var limit int
	var includeAttempts bool

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Query session history and past execution attempts",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHistory(cmd, opts, historyOptions{
				agentID:         agentID,
				workspaceID:     workspaceID,
				status:          status,
				since:           since,
				limit:           limit,
				includeAttempts: includeAttempts,
				jsonOutput:      jsonOutput,
			})
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	cmd.Flags().StringVarP(&agentID, "agent", "a", "", "filter by agent ID")
	cmd.Flags().StringVarP(&workspaceID, "workspace", "w", "", "filter by workspace ID or path")
	cmd.Flags().StringVarP(&status, "status", "s", "", "filter by status (active, finished, all)")
	cmd.Flags().StringVar(&since, "since", "", "filter sessions created since timestamp (RFC3339)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "max sessions to return")
	cmd.Flags().BoolVar(&includeAttempts, "attempts", false, "display attempt execution history")

	cmd.AddCommand(historyPruneCommand(opts))
	return cmd
}

type historyOptions struct {
	agentID         string
	workspaceID     string
	status          string
	since           string
	limit           int
	includeAttempts bool
	jsonOutput      bool
}

func runHistory(cmd *cobra.Command, opts Options, flags historyOptions) error {
	client, err := readClient(cmd.Context(), opts)
	if err != nil {
		return err
	}

	req := app.HistoryRequest{
		AgentID:         flags.agentID,
		WorkspaceID:     flags.workspaceID,
		Status:          flags.status,
		Since:           flags.since,
		Limit:           flags.limit,
		IncludeAttempts: flags.includeAttempts,
	}

	res, err := client.History(cmd.Context(), req)
	if err != nil {
		return err
	}

	if flags.jsonOutput {
		return writeJSON(opts.Stdout, res)
	}

	if len(res.Sessions) == 0 {
		fmt.Fprintln(opts.Stdout, "No session history found.")
		return nil
	}

	tw := tabwriter.NewWriter(opts.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION ID\tNAME\tAGENT\tSTATUS\tATTEMPTS\tCREATED\tWORKSPACE")

	for _, s := range res.Sessions {
		createdStr := s.CreatedAt.Local().Format("2006-01-02 15:04:05")
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			s.ID, s.Name, s.AgentID, s.Lifecycle, s.AttemptCount, createdStr, s.WorkspaceID)

		if flags.includeAttempts && len(s.Attempts) > 0 {
			for _, a := range s.Attempts {
				dur := a.Duration
				if dur == "" {
					dur = "-"
				}
				exit := "-"
				if a.HasExitCode {
					exit = fmt.Sprintf("%d", a.ExitCode)
				}
				reason := a.Reason
				if reason == "" {
					reason = "-"
				}
				startStr := a.StartedAt.Local().Format("15:04:05")
				fmt.Fprintf(tw, "  └─ gen %d\t\t\t%s\t\t%s (%s)\texit=%s %s\n",
					a.Generation, a.Lifecycle, startStr, dur, exit, reason)
			}
		}
	}
	return tw.Flush()
}

func historyPruneCommand(opts Options) *cobra.Command {
	var dryRun bool
	var maxAttempts int
	var maxEvents int
	var maxEventAge, maxFinishedAge string

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune old terminal attempts and events according to retention policy",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := opts.Paths
			if paths.StateDir == "" {
				p, err := config.ResolvePaths(func(k string) string { return "" })
				if err == nil {
					paths = p
				}
			}

			cfg := opts.Config
			if file := paths.ConfigFile(); file != "" {
				if loaded, err := config.Load(file); err == nil {
					cfg = loaded
				}
			}

			attLimit := cfg.Retention.MaxAttemptsPerSession
			if maxAttempts > 0 {
				attLimit = maxAttempts
			}

			evtLimit := cfg.Retention.MaxEvents
			if maxEvents > 0 {
				evtLimit = maxEvents
			}

			evtAge, _ := cfg.Retention.EventAgeDuration()
			if maxEventAge != "" {
				d, err := time.ParseDuration(maxEventAge)
				if err != nil {
					return fmt.Errorf("invalid max-event-age: %w", err)
				}
				evtAge = d
			}

			sessAge, _ := cfg.Retention.FinishedSessionAgeDuration()
			if maxFinishedAge != "" {
				d, err := time.ParseDuration(maxFinishedAge)
				if err != nil {
					return fmt.Errorf("invalid max-finished-session-age: %w", err)
				}
				sessAge = d
			}

			if dryRun {
				fmt.Fprintln(opts.Stdout, "[DRY RUN] Prune retention policy check:")
				fmt.Fprintf(opts.Stdout, "  Max attempts per session: %d\n", attLimit)
				fmt.Fprintf(opts.Stdout, "  Max events in journal:    %d\n", evtLimit)
				fmt.Fprintf(opts.Stdout, "  Max event age TTL:        %s\n", evtAge)
				fmt.Fprintf(opts.Stdout, "  Max finished session TTL: %s\n", sessAge)
				return nil
			}

			lockPath := filepath.Join(paths.StateDir, ".lock")
			sessionsPath := filepath.Join(paths.StateDir, "sessions.json")
			eventsPath := filepath.Join(paths.StateDir, "events.jsonl")

			now := time.Now().UTC()

			// 1. Prune sessions store
			sessStore := store.NewSessionsStore(sessionsPath, lockPath)
			prunedAtt, prunedSess, err := sessStore.PruneRetention(cmd.Context(), attLimit, sessAge, now)
			if err != nil && !strings.Contains(err.Error(), "no such file") {
				return fmt.Errorf("prune sessions: %w", err)
			}

			// 2. Prune event journal
			journal, err := store.NewEventJournal(eventsPath, lockPath)
			prunedEvt := 0
			if err == nil {
				p, err := journal.Prune(cmd.Context(), evtLimit, evtAge, now)
				if err != nil && !strings.Contains(err.Error(), "no such file") {
					return fmt.Errorf("prune event journal: %w", err)
				}
				prunedEvt = p
			}

			fmt.Fprintf(opts.Stdout, "Retention pruning completed successfully:\n")
			fmt.Fprintf(opts.Stdout, "  Pruned attempts: %d\n", prunedAtt)
			fmt.Fprintf(opts.Stdout, "  Pruned sessions: %d\n", prunedSess)
			fmt.Fprintf(opts.Stdout, "  Pruned events:   %d\n", prunedEvt)
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "simulate pruning without modifying disk")
	cmd.Flags().IntVar(&maxAttempts, "max-attempts", 0, "override max attempts per session")
	cmd.Flags().IntVar(&maxEvents, "max-events", 0, "override max events in journal")
	cmd.Flags().StringVar(&maxEventAge, "max-event-age", "", "override max event age duration (e.g. 720h)")
	cmd.Flags().StringVar(&maxFinishedAge, "max-finished-session-age", "", "override finished session age duration (e.g. 2160h)")

	return cmd
}
