//go:build linux

package cli

import (
	"fmt"
	"strings"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
	"github.com/spf13/cobra"
)

func workspaceCommand(opts Options) *cobra.Command {
	var jsonOutput bool
	var lifecycle, agentID string

	cmd := &cobra.Command{
		Use:     "workspace",
		Aliases: []string{"workspaces"},
		Short:   "List and inspect session workspaces",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceList(cmd, opts, jsonOutput, lifecycle, agentID)
		},
	}

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List workspaces with session summaries",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceList(cmd, opts, jsonOutput, lifecycle, agentID)
		},
	}

	for _, c := range []*cobra.Command{cmd, listCmd} {
		c.Flags().BoolVar(&jsonOutput, "json", false, "write stable JSON to stdout")
		c.Flags().StringVarP(&lifecycle, "lifecycle", "l", "", "filter by session lifecycle")
		c.Flags().StringVarP(&agentID, "agent", "a", "", "filter by agent ID")
	}

	cmd.AddCommand(listCmd)
	return cmd
}

func runWorkspaceList(cmd *cobra.Command, opts Options, jsonOutput bool, lifecycle, agentID string) error {
	client, err := readClient(cmd.Context(), opts)
	if err != nil {
		return err
	}

	req := app.WorkspacesRequest{
		AgentID: agent.ID(agentID),
	}
	if lifecycle != "" {
		req.Lifecycles = []session.Lifecycle{session.Lifecycle(lifecycle)}
	}

	result, err := client.Workspaces(cmd.Context(), req)
	if err != nil {
		return err
	}

	if jsonOutput {
		return writeJSON(opts.Stdout, workspacesDocumentOf(result))
	}

	if len(result.Workspaces) == 0 {
		fmt.Fprintln(opts.Stdout, "No workspaces found.")
		return nil
	}

	fmt.Fprintln(opts.Stdout, "WORKSPACE\tGIT\tACTIVE\tTOTAL\tSESSIONS")
	for _, ws := range result.Workspaces {
		gitInfo := "-"
		if ws.Git != nil {
			branch := ws.Git.Branch
			if ws.Git.Detached {
				branch = "detached"
			}
			if ws.Git.Dirty {
				branch += "*"
			}
			gitInfo = branch
		}
		pathDisplay := ws.Workspace.Path()
		if workspace.IsSubdirectoryOfGitRoot(ws.Workspace) {
			pathDisplay += " [subdir]"
		}
		sessionList := strings.Join(ws.SessionIDs, ", ")
		if sessionList == "" {
			sessionList = "-"
		}
		fmt.Fprintf(opts.Stdout, "%s\t%s\t%d\t%d\t%s\n",
			pathDisplay, gitInfo, ws.ActiveCount, ws.TotalCount, sessionList)
	}
	return nil
}
