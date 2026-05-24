package main

import (
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List orchestrators and their roles",
		Long:  "Print every orchestrator with its roles, each role's argus_project, and its current binding state (live | between incarnations | none).",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO(ludwig-v1 task 16.6)
			return errNotYetImplemented("ludwig list")
		},
	}
}
