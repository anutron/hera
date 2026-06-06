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
}

func (s railViewState) isEmpty() bool {
	return len(s.Collapsed) == 0 && len(s.FreelanceCollapsed) == 0 && len(s.ArchiveExpanded) == 0 && len(s.PinnedFreelance) == 0
}

// railStateJSON is the JSON wire format. JSON objects require string keys, so
// the int64-keyed maps are serialised as map[string]bool and converted on
// each side.
type railStateJSON struct {
	Collapsed          map[string]bool `json:"collapsed,omitempty"`
	FreelanceCollapsed map[string]bool `json:"freelance_collapsed,omitempty"`
	ArchiveExpanded    map[string]bool `json:"archive_expanded,omitempty"`
	PinnedFreelance    map[string]bool `json:"pinned_freelance,omitempty"`
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
		FreelanceCollapsed: j.FreelanceCollapsed,
		PinnedFreelance:    j.PinnedFreelance,
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
		FreelanceCollapsed: s.FreelanceCollapsed,
		PinnedFreelance:    s.PinnedFreelance,
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
