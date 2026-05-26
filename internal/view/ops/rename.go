package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// RenameOrchestrator handles the `r` rail operation when an orchestrator
// row is selected. The new name must be unique across non-archived
// orchestrators; archived rows with the same name do not block the
// rename (matching the hera-coordination delta spec).
//
// No argus side effects — argus task names are independent of hera
// role/orchestrator names per design.md D5 + Risks.
func (s *Service) RenameOrchestrator(ctx context.Context, id int64, newName string) error {
	name := strings.TrimSpace(newName)
	if name == "" {
		return validation("name is required")
	}

	cur, err := s.DB.GetOrchestratorByID(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.RenameOrchestrator: load %d: %w", id, err)
	}
	if cur.Name == name {
		// No-op rename. Treat as success; nothing to do.
		return nil
	}

	other, err := s.DB.GetOrchestratorByName(ctx, name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("ops.RenameOrchestrator: uniqueness lookup: %w", err)
	}
	if other != nil && other.ID != id && !other.Archived {
		return validation(fmt.Sprintf("orchestrator %q already exists", name))
	}

	if err := s.DB.RenameOrchestrator(ctx, id, name); err != nil {
		return fmt.Errorf("ops.RenameOrchestrator: %w", err)
	}
	return nil
}

// RenameRole handles the `r` rail operation when a role row is selected.
// The new name must be unique within the role's orchestrator across
// non-archived roles; archived siblings with the same name do not
// block, and the same role name may coexist across orchestrators.
//
// No argus side effects — argus task names are independent of hera role
// names per design.md D5.
func (s *Service) RenameRole(ctx context.Context, id int64, newName string) error {
	name := strings.TrimSpace(newName)
	if name == "" {
		return validation("name is required")
	}

	cur, err := s.DB.GetRoleByID(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.RenameRole: load %d: %w", id, err)
	}
	if cur.Name == name {
		return nil
	}

	siblings, err := s.DB.ListRolesByOrchestrator(ctx, cur.OrchestratorID)
	if err != nil {
		return fmt.Errorf("ops.RenameRole: list siblings: %w", err)
	}
	for _, r := range siblings {
		if r.ID == id {
			continue
		}
		if !r.Archived && r.Name == name {
			return validation(fmt.Sprintf("role %q already exists in this orchestrator", name))
		}
	}

	if err := s.DB.RenameRole(ctx, id, name); err != nil {
		return fmt.Errorf("ops.RenameRole: %w", err)
	}
	return nil
}
