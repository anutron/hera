package main

import (
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	var foreground bool

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the ludwig daemon",
		Long:  "Start the ludwig daemon. With --foreground, runs in the current shell (useful for dev). Without it, double-forks into the background and writes its PID to ~/.ludwig/ludwig.pid.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO(ludwig-v1 task 16.3): wire daemon.Run(ctx, cfg).
			return errNotYetImplemented("ludwig start")
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run in the foreground instead of daemonizing")
	return cmd
}
