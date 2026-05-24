package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/anutron/hera/internal/config"
)

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running hera daemon",
		Long:  "Read the PID from ~/.hera/hera.pid, send SIGTERM, and wait up to 10 seconds for the daemon to exit cleanly.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Default()
			data, err := os.ReadFile(cfg.PIDPath())
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("no PID file at %s; is hera running?", cfg.PIDPath())
				}
				return fmt.Errorf("read pidfile: %w", err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				return fmt.Errorf("malformed pidfile (%q): %w", string(data), err)
			}
			proc, err := os.FindProcess(pid)
			if err != nil {
				return err
			}
			if err := proc.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf("signal pid %d: %w", pid, err)
			}

			// Wait up to 10s for the process to exit (PID file should be removed).
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(cfg.PIDPath()); os.IsNotExist(err) {
					fmt.Fprintf(cmd.OutOrStdout(), "hera (pid %d) stopped.\n", pid)
					return nil
				}
				time.Sleep(200 * time.Millisecond)
			}
			return fmt.Errorf("hera (pid %d) did not exit within 10s", pid)
		},
	}
}
