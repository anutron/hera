package main

import (
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the ludwig daemon status",
		Long:  "Print whether the daemon is running, last-seen event id, MCP tool registration status, and counts of orchestrators / roles / live bindings.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO(ludwig-v1 task 16.5)
			return errNotYetImplemented("ludwig status")
		},
	}
}
