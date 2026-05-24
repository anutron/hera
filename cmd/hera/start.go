package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/anutron/hera/internal/config"
	"github.com/anutron/hera/internal/daemon"
)

func newStartCmd() *cobra.Command {
	var foreground bool

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the hera daemon",
		Long:  "Start the hera daemon. --foreground keeps it in the current shell (useful for dev). Without it, hera exits after writing its PID to ~/.hera/hera.pid (true daemonization with double-fork is deferred to a follow-up; until then, run via launchd or `nohup hera start --foreground &`).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Default()

			// In v1, --foreground is the only supported mode. Background
			// daemonization (double-fork or launchd plist) is intentionally
			// deferred; the user can wrap with `nohup` or a launchd job.
			if !foreground {
				return fmt.Errorf("hera start without --foreground is not yet implemented; pass --foreground (you can wrap the call in nohup or launchd if you need it backgrounded)")
			}

			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			return daemon.Run(ctx, cfg, logger)
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run in the foreground instead of daemonizing")
	return cmd
}
