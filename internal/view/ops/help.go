package ops

// HelpBinding is one line in the help modal: a key combo and what it
// does in a particular focus state.
type HelpBinding struct {
	Key  string
	Desc string
}

// HelpSection is one focus-state group of bindings (RAIL, COORD, AGENT,
// or "Modal" for the help/confirm modals themselves).
type HelpSection struct {
	Title    string
	Bindings []HelpBinding
}

// HelpContent returns the static help-modal content grouped by focus
// state. Keys are sourced from design.md D4 + D5; the modal layer
// formats the result into a tview Modal. No DB read, no argus call.
//
// The dismiss key (`q`) is documented on the Modal section so the
// operator knows how to close the modal without trying Esc (which
// argus intercepts).
func HelpContent() []HelpSection {
	return []HelpSection{
		{
			Title: "Focus traversal",
			Bindings: []HelpBinding{
				{"Cmd/Ctrl-→", "Advance focus (RAIL → COORD → AGENT)"},
				{"Cmd/Ctrl-←", "Retreat focus (AGENT → COORD → RAIL)"},
				{"Enter", "From RAIL: jump to AGENT (skips COORD)"},
				{"Ctrl-Q", "Return to RAIL from any focus"},
			},
		},
		{
			Title: "RAIL navigation",
			Bindings: []HelpBinding{
				{"j / ↓", "Next entry"},
				{"k / ↑", "Previous entry"},
				{"Enter", "Jump to AGENT pane for selected agent"},
				{"Enter", "Resurrect when Archive is visible and an archived coord is selected"},
			},
		},
		{
			Title: "RAIL mutations",
			Bindings: []HelpBinding{
				{"n", "New project — prompts for name + mission"},
				{"r", "Rename — orchestrator or role"},
				{"^d", "Delete — ends binding, archives role, removes worktree"},
				{"a", "Archive toggle — preserves worktree"},
				{"l", "List-all toggle — show/hide the Archive section"},
				{"?", "Show this help"},
			},
		},
		{
			Title: "COORD / AGENT panes",
			Bindings: []HelpBinding{
				{"any key", "Forwarded to the bound argus task's PTY input"},
				{"n / r / ^d / a / l / ?", "Typed literally — mutation keys are RAIL-only"},
			},
		},
		{
			Title: "This modal",
			Bindings: []HelpBinding{
				{"q", "Close (Esc is reserved by argus)"},
			},
		},
	}
}
