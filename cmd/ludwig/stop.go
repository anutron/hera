package main

import (
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running ludwig daemon",
		Long:  "Read the PID from ~/.ludwig/ludwig.pid, send SIGTERM, and wait up to 10 seconds for the daemon to exit cleanly.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO(ludwig-v1 task 16.4)
			return errNotYetImplemented("ludwig stop")
		},
	}
}
