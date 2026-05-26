package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// NewOrchestratorInput is the (validated) result of confirming the
// new-project modal.
type NewOrchestratorInput struct {
	// Name is the orchestrator name. Must be non-empty and unique among
	// non-archived orchestrators.
	Name string
	// Mission is the coordinator role's mission (free-form prose).
	// Optional; an empty string is allowed.
	Mission string
}

// NewOrchestrator handles the `n` rail operation. It validates the modal
// input and spawns an argus task whose first action is a
// hera_new_orchestrator() MCP call. The orchestrator + coord role +
// binding rows are NOT created here — they are created by hera's
// existing hera_new_orchestrator handler when the spawned task runs.
//
// Per design.md D5: "The view MUST NOT directly insert orchestrator /
// role / binding rows; those rows MUST be created by the existing
// hera_new_orchestrator handler when the spawned task makes its first
// MCP call." This keeps the row-creation logic in exactly one place
// (the MCP handler) instead of duplicating it across surfaces.
//
// Returns ErrValidation for user-correctable input problems (empty or
// duplicate name); any other error is a substrate failure (DB read or
// argus POST) that the caller surfaces to the operator.
func (s *Service) NewOrchestrator(ctx context.Context, in NewOrchestratorInput) (*CreatedTask, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, validation("name is required")
	}

	// Uniqueness: only against non-archived orchestrators. Archived rows
	// with the same name do not block creation — that's the
	// "archived orchestrator with same name does not block creation"
	// scenario from the hera-coordination delta spec.
	existing, err := s.DB.GetOrchestratorByName(ctx, name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("ops.NewOrchestrator: lookup: %w", err)
	}
	if existing != nil && !existing.Archived {
		return nil, validation(fmt.Sprintf("orchestrator %q already exists", name))
	}

	mission := strings.TrimSpace(in.Mission)
	prompt := buildBootstrapPrompt(name, mission)

	req := CreateTaskRequest{
		Project: name,
		Name:    name + "-coord",
		Prompt:  prompt,
	}
	task, err := s.Argus.CreateTask(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ops.NewOrchestrator: argus CreateTask: %w", err)
	}
	return task, nil
}

// buildBootstrapPrompt assembles the argus-task prompt that drives the
// new coord into a hera_new_orchestrator MCP call. The mission is
// embedded verbatim; an empty mission is rendered as the empty string
// (matching the hera-coordination spec's optional-mission contract).
//
// Format is stable because spec scenarios assert on it (see
// hera-view delta spec: "New-project confirm spawns argus task" —
// asserts the prompt contains the literal hera_new_orchestrator call
// with the given name + mission).
func buildBootstrapPrompt(name, mission string) string {
	// We escape any embedded quotes in the user-supplied strings via a
	// simple replace so the literal expression we emit stays parseable.
	// The MCP layer doesn't actually re-parse this — it's prose for the
	// Claude session — but the spec asserts a literal substring, and we
	// want the substring to be valid Go-string-syntax-shaped for the
	// reader's sanity.
	q := func(s string) string { return strings.ReplaceAll(s, `"`, `\"`) }
	return fmt.Sprintf(
		`You are the coordinator for hera orchestrator %q. As your first action call: hera_new_orchestrator(cwd=$PWD, name="%s", coord_role_name="coord", mission="%s")`,
		name, q(name), q(mission),
	)
}
