package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/anutron/ludwig/internal/config"
	"github.com/anutron/ludwig/internal/db"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List orchestrators and their roles",
		Long:  "Print every orchestrator with its roles, each role's argus_project, and current binding state (live | none).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Default()
			if _, err := os.Stat(cfg.StatePath()); os.IsNotExist(err) {
				fmt.Fprintln(cmd.OutOrStdout(), "(no orchestrators – ludwig has never started)")
				return nil
			}
			database, err := db.Open(cfg.StatePath())
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer database.Close()

			ctx := context.Background()
			orchs, err := database.Orchestrators.List(ctx)
			if err != nil {
				return err
			}
			if len(orchs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no orchestrators)")
				return nil
			}
			for _, o := range orchs {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", o.Name)
				roles, err := database.Roles.ListByOrchestrator(ctx, o.ID)
				if err != nil {
					return err
				}
				for _, r := range roles {
					state := "none"
					if bnd, err := database.Bindings.GetLiveByRole(ctx, r.ID); err == nil {
						state = fmt.Sprintf("live (task %s)", bnd.ArgusTaskID)
					} else if !errors.Is(err, db.ErrNotFound) {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "  - %s [%s, %s]: %s\n", r.Name, r.Kind, r.ArgusProject, state)
				}
			}
			return nil
		},
	}
}
