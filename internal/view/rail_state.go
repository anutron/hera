package view

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/anutron/hera/internal/db"
)

// railStateConfigKey is the config-table key for the persisted rail fold state.
const railStateConfigKey = "view.rail.state"

// railLastSelection records the stable identity of the rail row the operator
// most recently rested on. Used to restore the cursor after a hera-view open
// so the operator lands back on the same row rather than resetting to the top
// (BUG-001).
//
// Three identifiers are stored because different row types have different
// stable keys: managed roles have a DB RoleID; freelancers have RoleID==0 but
// a stable ArgusTaskID; orchestrator headers have an OrchID. The restore
// attempt tries each in order (RoleID → ArgusTaskID → OrchID) and falls back
// to the topmost live item when none match.
type railLastSelection struct {
	RoleID      int64  // > 0 for managed role rows
	ArgusTaskID string // non-empty for freelancers and as fallback for managed roles
	OrchID      int64  // > 0 for orchestrator header rows
}

// isZero reports whether the selection carries no identity — the zero value
// representing "no prior selection" (first ever open, or daemon restarted with
// no state).
func (s railLastSelection) isZero() bool {
	return s.RoleID == 0 && s.ArgusTaskID == "" && s.OrchID == 0
}

// railViewState is a snapshot of the rail's operator-set fold choices.
// These survive daemon restarts so the operator does not have to re-collapse
// coordinators or re-open archive expandos after each hera restart.
type railViewState struct {
	Collapsed          map[int64]bool
	FreelanceCollapsed map[string]bool
	ArchiveExpanded    map[int64]bool
	// PinnedFreelance tracks which freelance tasks (keyed by ArgusTaskID) the
	// operator has pinned. Freelancers have no hera DB row, so their pin state
	// lives here alongside FreelanceCollapsed rather than in the roles table.
	PinnedFreelance map[string]bool
	// ArchiveGroupExpanded tracks which sub-groups inside the consolidated bottom
	// Archive are folded open, keyed by group name ("Hera sessions", "ARGUS", …).
	// Default (zero value) is collapsed — sub-groups stay tucked until the
	// operator expands them (BUG-026).
	ArchiveGroupExpanded map[string]bool
	// LastSelection is the identity of the row the operator was on when the
	// state was last saved. Restored on the next hera-view open (BUG-001).
	LastSelection railLastSelection
}

func (s railViewState) isEmpty() bool {
	return len(s.Collapsed) == 0 && len(s.FreelanceCollapsed) == 0 &&
		len(s.ArchiveExpanded) == 0 && len(s.PinnedFreelance) == 0 &&
		len(s.ArchiveGroupExpanded) == 0
}

// railStateJSON is the JSON wire format. JSON objects require string keys, so
// the int64-keyed maps are serialised as map[string]bool and converted on
// each side.
type railStateJSON struct {
	Collapsed            map[string]bool `json:"collapsed,omitempty"`
	FreelanceCollapsed   map[string]bool `json:"freelance_collapsed,omitempty"`
	ArchiveExpanded      map[string]bool `json:"archive_expanded,omitempty"`
	PinnedFreelance      map[string]bool `json:"pinned_freelance,omitempty"`
	ArchiveGroupExpanded map[string]bool `json:"archive_group_expanded,omitempty"`
	// LastSelection fields (BUG-001). Stored flat (no nested object) so
	// omitempty elides the whole group when the selection is zero.
	LastSelRoleID      int64  `json:"last_sel_role_id,omitempty"`
	LastSelArgusTaskID string `json:"last_sel_argus_task_id,omitempty"`
	LastSelOrchID      int64  `json:"last_sel_orch_id,omitempty"`
}

func loadRailStateFromDB(ctx context.Context, cfg *db.ConfigDAO) (railViewState, error) {
	raw, err := cfg.Get(ctx, railStateConfigKey)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return railViewState{}, nil
		}
		return railViewState{}, err
	}
	var j railStateJSON
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		return railViewState{}, err
	}
	s := railViewState{
		FreelanceCollapsed:   j.FreelanceCollapsed,
		PinnedFreelance:      j.PinnedFreelance,
		ArchiveGroupExpanded: j.ArchiveGroupExpanded,
		LastSelection: railLastSelection{
			RoleID:      j.LastSelRoleID,
			ArgusTaskID: j.LastSelArgusTaskID,
			OrchID:      j.LastSelOrchID,
		},
	}
	if len(j.Collapsed) > 0 {
		s.Collapsed = make(map[int64]bool, len(j.Collapsed))
		for k, v := range j.Collapsed {
			if id, err := strconv.ParseInt(k, 10, 64); err == nil {
				s.Collapsed[id] = v
			}
		}
	}
	if len(j.ArchiveExpanded) > 0 {
		s.ArchiveExpanded = make(map[int64]bool, len(j.ArchiveExpanded))
		for k, v := range j.ArchiveExpanded {
			if id, err := strconv.ParseInt(k, 10, 64); err == nil {
				s.ArchiveExpanded[id] = v
			}
		}
	}
	return s, nil
}

func saveRailStateToDB(ctx context.Context, cfg *db.ConfigDAO, s railViewState) error {
	j := railStateJSON{
		FreelanceCollapsed:   s.FreelanceCollapsed,
		PinnedFreelance:      s.PinnedFreelance,
		ArchiveGroupExpanded: s.ArchiveGroupExpanded,
		LastSelRoleID:        s.LastSelection.RoleID,
		LastSelArgusTaskID:   s.LastSelection.ArgusTaskID,
		LastSelOrchID:        s.LastSelection.OrchID,
	}
	if len(s.Collapsed) > 0 {
		j.Collapsed = make(map[string]bool, len(s.Collapsed))
		for id, v := range s.Collapsed {
			j.Collapsed[strconv.FormatInt(id, 10)] = v
		}
	}
	if len(s.ArchiveExpanded) > 0 {
		j.ArchiveExpanded = make(map[string]bool, len(s.ArchiveExpanded))
		for id, v := range s.ArchiveExpanded {
			j.ArchiveExpanded[strconv.FormatInt(id, 10)] = v
		}
	}
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return cfg.Set(ctx, railStateConfigKey, string(data))
}
