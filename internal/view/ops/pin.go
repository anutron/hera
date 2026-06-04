package ops

import (
	"context"
	"fmt"
)

// PinRole handles the PIN verb against a role (the `P` key): set the role's
// hera pinned_at AND clear its archived_at (the DAO does both atomically —
// pin and archive are mutually exclusive, mirroring argus's SetPinned).
// Because pinning clears the archived state, it MUST also clear the argus
// side: a role whose bound task was argus-archived would otherwise still
// render in an Archive expando (the rail buckets on EITHER side). We reuse
// the symmetric unarchive path (live binding preferred, latest-binding
// fallback) so a pinned row is never argus-archived. A 404 (pruned task) is
// tolerated as a skip, like the archive verbs.
func (s *Service) PinRole(ctx context.Context, id int64) error {
	if err := s.DB.PinRole(ctx, id); err != nil {
		return fmt.Errorf("ops.PinRole: pin: %w", err)
	}
	return s.unarchiveBoundArgusTask(ctx, id, "PinRole")
}

// UnpinRole clears the role's pinned_at. No argus side — unpinning does not
// change the archived state, so the bound task is left as-is.
func (s *Service) UnpinRole(ctx context.Context, id int64) error {
	if err := s.DB.UnpinRole(ctx, id); err != nil {
		return fmt.Errorf("ops.UnpinRole: unpin: %w", err)
	}
	return nil
}

// PinOrchestrator handles the PIN verb against an orchestrator header: set
// the orchestrator's pinned_at AND clear its archived_at (DAO-atomic). As
// with UnarchiveOrchestrator, pinning also unarchives the coord role's bound
// argus task so the pinned coordinator's pane is live (the coord task is the
// orchestrator's argus face). Worker roles are NOT touched — pinning an
// orchestrator pins only the header (its children render nested beneath it).
func (s *Service) PinOrchestrator(ctx context.Context, id int64) error {
	if err := s.DB.PinOrchestrator(ctx, id); err != nil {
		return fmt.Errorf("ops.PinOrchestrator: pin: %w", err)
	}
	// The inclusive list is required — the coord role may itself be archived
	// at this point (a previously-archived orchestrator being pinned), so the
	// default (active-only) list would miss it.
	roles, err := s.DB.ListRolesByOrchestratorInclusive(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.PinOrchestrator: list roles: %w", err)
	}
	for _, role := range roles {
		if role.Kind != KindCoordinator {
			continue
		}
		return s.unarchiveBoundArgusTask(ctx, role.ID, "PinOrchestrator")
	}
	return nil
}

// UnpinOrchestrator clears the orchestrator's pinned_at. No argus side.
func (s *Service) UnpinOrchestrator(ctx context.Context, id int64) error {
	if err := s.DB.UnpinOrchestrator(ctx, id); err != nil {
		return fmt.Errorf("ops.UnpinOrchestrator: unpin: %w", err)
	}
	return nil
}
