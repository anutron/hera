// Command ludwig is a coordinator/overlay daemon for argus. It registers as
// an argus plugin via a scope token, owns role-as-identity coordination
// (orchestrators, roles, bindings, messages) in its own SQLite, and exposes a
// five-tool MCP surface to agents inside argus tasks.
//
// See openspec/changes/ludwig-v1/design.md for the design doc and the
// openspec/changes/ludwig-v1/specs/ludwig-coordination/spec.md delta spec
// for the behavioral requirements.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version is the ludwig binary version. Set via -ldflags at build time:
//
//	go build -ldflags "-X main.Version=v0.1.0"
var Version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "ludwig",
		Short:         "Coordinator/overlay daemon for argus",
		Long:          "ludwig is an argus plugin that owns role-as-identity coordination on top of argus's task layer. It persists orchestrators, roles, and messages in its own SQLite state and delivers messages between agents via an idle-gated PTY injection bus.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newStartCmd())
	root.AddCommand(newStopCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newResumeCmd())

	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "ludwig: %v\n", err)
		os.Exit(1)
	}
}
