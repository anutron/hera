package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/anutron/ludwig/internal/config"
	"github.com/anutron/ludwig/internal/db"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the ludwig daemon status",
		Long:  "Print whether the daemon is running, the last-seen event id, and counts of orchestrators / roles / live bindings.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Default()

			running, pid := pidIsAlive(cfg)
			if running {
				fmt.Fprintf(cmd.OutOrStdout(), "ludwig: running (pid %d)\n", pid)
			} else if pid != 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "ludwig: stale pidfile (pid %d not alive)\n", pid)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "ludwig: not running")
			}

			// Open the DB read-only-ish (we run migrations but no real
			// writes) just to read summary counts. Skip if the DB doesn't
			// exist yet.
			if _, err := os.Stat(cfg.StatePath()); os.IsNotExist(err) {
				fmt.Fprintln(cmd.OutOrStdout(), "state: (no SQLite yet)")
				return nil
			}

			database, err := db.Open(cfg.StatePath())
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer database.Close()

			ctx := context.Background()
			orchs, _ := database.Orchestrators.List(ctx)
			cursor, _ := database.EventCursor.Get(ctx)
			fmt.Fprintf(cmd.OutOrStdout(), "argus_url:        %s\n", cfg.ArgusBaseURL)
			fmt.Fprintf(cmd.OutOrStdout(), "mcp_addr:         %s\n", cfg.ListenAddr)
			fmt.Fprintf(cmd.OutOrStdout(), "state_path:       %s\n", cfg.StatePath())
			fmt.Fprintf(cmd.OutOrStdout(), "last_event_id:    %d\n", cursor)
			fmt.Fprintf(cmd.OutOrStdout(), "orchestrator_cnt: %d\n", len(orchs))
			for _, o := range orchs {
				roles, _ := database.Roles.ListByOrchestrator(ctx, o.ID)
				live := 0
				for _, r := range roles {
					if _, err := database.Bindings.GetLiveByRole(ctx, r.ID); err == nil {
						live++
					}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  - %s: %d roles, %d live bindings\n", o.Name, len(roles), live)
			}
			return nil
		},
	}
}

// pidIsAlive checks whether the recorded PID points at a running process.
func pidIsAlive(cfg *config.Config) (bool, int) {
	data, err := os.ReadFile(cfg.PIDPath())
	if err != nil {
		return false, 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, 0
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, pid
	}
	// On Unix, Signal(0) reports whether the process is reachable.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false, pid
	}
	return true, pid
}
