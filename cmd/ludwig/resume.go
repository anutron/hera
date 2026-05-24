package main

import (
	"github.com/spf13/cobra"
)

func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume <orchestrator>:<role>",
		Short: "Spawn a fresh incarnation of an existing role",
		Long:  "DEFERRED to a follow-up change. Will create a new argus task in the role's stored argus_project and bind it to the named role, restoring the inbox and history.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO(ludwig-v1 task 16.7): stays a stub in v1 per the design doc.
			return errNotYetImplemented("ludwig resume (deferred from v1)")
		},
	}
}
