//go:build linux

package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/mcp"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/spf13/cobra"
)

func mcpCommand(opts Options) *cobra.Command {
	var (
		readOnly       bool
		allowMutations bool
		allowTools     []string
		workspaces     []string
	)

	runServe := func(cmd *cobra.Command, _ []string) error {
		socketPath, err := ipc.SocketPath(opts.Paths)
		if err != nil {
			return err
		}

		client := ipc.NewClient(socketPath)
		pingCtx, cancel := context.WithTimeout(cmd.Context(), 1500*time.Millisecond)
		defer cancel()

		if err := client.Ping(pingCtx); err != nil {
			return session.NewError(
				session.CodeOwnerUnavailable,
				"agentd",
				"the background agentd daemon is not running or not responding",
				"start agentd (e.g. 'agentd' or 'agentd start') before launching MCP control plane",
			)
		}

		mcpCfg := opts.Config.MCP
		if allowMutations {
			f := false
			mcpCfg.ReadOnly = &f
		} else if readOnly {
			t := true
			mcpCfg.ReadOnly = &t
		}

		if len(allowTools) > 0 {
			mcpCfg.AllowedTools = append(mcpCfg.AllowedTools, allowTools...)
		}
		if len(workspaces) > 0 {
			mcpCfg.AllowedWorkspaces = append(mcpCfg.AllowedWorkspaces, workspaces...)
		}

		policy, err := mcp.NewPolicyEngine(mcpCfg)
		if err != nil {
			return err
		}

		idempotency := mcp.NewIdempotencyManager(5 * time.Minute)
		audit := mcp.NewAuditLogger(opts.Stderr)

		srv := mcp.NewServer(mcp.ServerOptions{
			Name:        "agent-sessions-deck",
			Version:     cmd.Root().Version,
			Policy:      policy,
			Idempotency: idempotency,
			Audit:       audit,
		})

		mcp.RegisterReadTools(srv, client)
		mcp.RegisterMutationTools(srv, client)

		sigCtx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		fmt.Fprintf(opts.Stderr, "ASD MCP control plane server starting (read_only=%v)...\n", policy.IsReadOnly())
		return srv.Serve(sigCtx, opts.Stdin, opts.Stdout)
	}

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Model Context Protocol (MCP) control plane server for AI client integration",
		Long:  "mcp serves JSON-RPC 2.0 over stdio following the Model Context Protocol specification (2024-11-05). It connects as a client to agentd.",
		RunE:  runServe,
	}

	cmd.Flags().BoolVar(&readOnly, "read-only", true, "enforce read-only tool execution (reject mutations)")
	cmd.Flags().BoolVar(&allowMutations, "allow-mutations", false, "explicitly allow mutation tools (start/stop/restart/interact)")
	cmd.Flags().StringSliceVar(&allowTools, "allow-tool", nil, "whitelist specific tool names")
	cmd.Flags().StringSliceVar(&workspaces, "workspace", nil, "restrict allowed workspace directories")

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the stdio JSON-RPC MCP server",
		RunE:  runServe,
	}
	serveCmd.Flags().BoolVar(&readOnly, "read-only", true, "enforce read-only tool execution (reject mutations)")
	serveCmd.Flags().BoolVar(&allowMutations, "allow-mutations", false, "explicitly allow mutation tools")
	serveCmd.Flags().StringSliceVar(&allowTools, "allow-tool", nil, "whitelist specific tool names")
	serveCmd.Flags().StringSliceVar(&workspaces, "workspace", nil, "restrict allowed workspace directories")

	cmd.AddCommand(serveCmd)
	return cmd
}
