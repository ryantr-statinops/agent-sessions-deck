//go:build linux

package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/spf13/cobra"
)

func eventsCommand(opts Options) *cobra.Command {
	var jsonOutput bool
	var sessionID, eventType string
	var afterRevision uint64
	var limit int

	cmd := &cobra.Command{
		Use:   "events",
		Short: "Query and inspect persistent metadata events",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := readClient(cmd.Context(), opts)
			if err != nil {
				return err
			}

			req := app.EventsRequest{
				SessionID:     sessionID,
				Type:          eventType,
				AfterRevision: afterRevision,
				Limit:         limit,
			}

			res, err := client.Events(cmd.Context(), req)
			if err != nil {
				return err
			}

			if jsonOutput {
				return writeJSON(opts.Stdout, res)
			}

			if len(res.Events) == 0 {
				fmt.Fprintln(opts.Stdout, "No events found.")
				return nil
			}

			tw := tabwriter.NewWriter(opts.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "REVISION\tTYPE\tSESSION\tATTEMPT\tTIMESTAMP")

			for _, ev := range res.Events {
				attemptStr := "-"
				if gen, ok := ev.AttemptGeneration(); ok {
					attemptStr = fmt.Sprintf("%d", gen)
				}
				sessStr := string(ev.SessionID())
				if sessStr == "" {
					sessStr = "-"
				}
				tsStr := ev.Timestamp().Time().Local().Format("2006-01-02 15:04:05")
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n",
					ev.Revision(), ev.Type(), sessStr, attemptStr, tsStr)
			}
			return tw.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
	cmd.Flags().StringVarP(&sessionID, "session", "s", "", "filter events by session ID")
	cmd.Flags().StringVarP(&eventType, "type", "t", "", "filter events by event type (e.g. session.started)")
	cmd.Flags().Uint64Var(&afterRevision, "after", 0, "stream events strictly after revision cursor")
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "max events to return")

	return cmd
}
