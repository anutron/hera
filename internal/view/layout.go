package view

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// RailWidth is the fixed character width of the navigation rail column.
// See design.md D7.
const RailWidth = 36

// heraBackground is the single background color every hera-rendered surface
// uses, so the chrome (root, top bar, rail, pane frames, gaps, modals) matches
// the pane interiors exactly (BUG-001). The SDK terminalpane paints its
// emulator cells with tcell.StyleDefault — background tcell.ColorDefault, the
// terminal's own default — so the pane interiors read as the terminal black.
// Using that same tcell.ColorDefault for every other surface guarantees one
// uniform black: no tview default (the dark Color0 that rendered grey-blue)
// and no theme.ColorStatusBG (Color235 dark gray) bleeds through anywhere.
const heraBackground = tcell.ColorDefault

// coordDetailsHERAFlex / coordDetailsPaneFlex split the space right of the
// rail in coordinator mode: the HERA pane takes ~2/3, the Details pane ~1/3.
// Flex (not fixed) so the HERA pane is never starved at narrow terminals — at
// 80 cols (rail 36) HERA still keeps ~29 cols. Composed only in coordinator
// mode. See openspec change coord-details-pane.
const (
	coordDetailsHERAFlex = 2
	coordDetailsPaneFlex = 1
)

// layoutPieces holds references to the individual chrome and body
// primitives composed by buildLayout. The app keeps these around to
// drive updates (rail refresh, pane swap) without recomposing the Flex
// each time.
type layoutPieces struct {
	root  *tview.Flex
	rail  *railList
	coord *pinnedTerminalPane
	agent *pinnedTerminalPane
	// details is the coordinator Details pane. It is created once here but
	// composed into the body only in coordinator mode (refreshBody), so the
	// agent and freelance layouts are unchanged.
	details *detailsPane
	body    *tview.Flex
	// pages wraps the root layout so the mutation bridge can overlay
	// input / confirm / help / error modals via AddPage. The base
	// layout lives at page "base" (visible by default); each modal is
	// added/removed by name.
	pages *tview.Pages
}

// buildLayout composes the tview Flex tree (design.md D7, revised by D12,
// revised by BUG-031):
//
//	Body (cols):
//	  Rail      (width RailWidth)
//	  CoordPane (flex 1, title "Coord")
//	  AgentPane (flex 1, title "Agent")
//
// Coord and agent panes split the remaining horizontal space evenly. There is
// no top-bar row: the body fills the full terminal height so the layout is
// flush (no internal margin) matching argus's own task view. The BUG-027
// fullscreen indicator lives in the coord/agent pane-title slot (set by
// OnFullscreenChanged), not a separate row. Hera renders NO bottom-bar row of
// its own — under the argus key-surrender contract (D12) argus draws the
// plugin-mode status bar from the focus-aware hotkey dictionary hera pushes
// over the WebSocket. The two terminalpanes own their own border and title
// rendering, so this layer only wires titles and arranges them in the Flex.
func buildLayout(coord, agent *pinnedTerminalPane) layoutPieces {
	rail := newRailList()
	rail.SetBackgroundColor(heraBackground)
	details := newDetailsPane()
	details.SetBackgroundColor(heraBackground)

	// Pane Box backgrounds match the emulator interior so the border/title
	// cells and any letterboxed gap inside a pane read as the same black.
	coord.SetBackgroundColor(heraBackground)
	agent.SetBackgroundColor(heraBackground)

	coord.SetTitle("Coord")
	agent.SetTitle("Agent")

	// Default composition is the agent-mode split (rail + HERA + AGENT); the
	// Details pane is added by refreshBody only when a coordinator is selected.
	body := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(rail, RailWidth, 0, false).
		AddItem(coord, 0, 1, false).
		AddItem(agent, 0, 1, false)
	// Flex containers paint their own background in any cells not covered by a
	// child (the canvas between/around panes); pin it to the same black.
	body.SetBackgroundColor(heraBackground)

	pages := tview.NewPages().
		AddPage(pageBase, body, true, true)

	return layoutPieces{
		root:    body,
		rail:    rail,
		coord:   coord,
		agent:   agent,
		details: details,
		body:    body,
		pages:   pages,
	}
}

// helpHotkeyItems returns the comprehensive hotkey dictionary for the ? help
// overlay: Rail, Coord pane, and Agent pane shortcuts in a flat list with
// labeled section-separator items. All items carry Bar:false so argus's bottom
// bar is unaffected during the brief push-before-help window; App.SendHelp
// restores the current-focus hotkeys after {"type":"help"} so the bar is
// correct when the overlay is dismissed. The COORD section is omitted when
// coordPresent is false (freelance / no-coord mode).
func helpHotkeyItems(coordPresent bool) []HotkeyItem {
	var items []HotkeyItem

	// appendSection appends src items with Bar forced to false — the help
	// overlay shows the complete keyset; Bar is only meaningful on the bottom
	// bar, not in the overlay.
	appendSection := func(src []HotkeyItem) {
		for _, it := range src {
			items = append(items, HotkeyItem{Key: it.Key, Label: it.Label})
		}
	}

	// Rail section.
	items = append(items, HotkeyItem{Key: "[ Rail ]"})
	appendSection(hotkeyItems(FocusRAIL, coordPresent))

	// Coord pane section — only when a coord pane exists.
	if coordPresent {
		items = append(items, HotkeyItem{}) // blank spacer
		items = append(items, HotkeyItem{Key: "[ Coord pane ]"})
		appendSection(hotkeyItems(FocusCOORD, coordPresent))
	}

	// Agent pane section.
	items = append(items, HotkeyItem{}) // blank spacer
	items = append(items, HotkeyItem{Key: "[ Agent pane ]"})
	appendSection(hotkeyItems(FocusAGENT, coordPresent))

	// Rail mutation keys (n/r/^d/a/l/w/J/P/^r/^p) are RAIL-focus-only; in a
	// pane they forward as literal bytes to the PTY.
	items = append(items, HotkeyItem{})
	items = append(items, HotkeyItem{Key: "(pane note)", Label: "n/r/^d/a/l/w/J/P/^r/^p → PTY in pane"})

	return items
}

// hotkeyItems returns the focus-aware hotkey dictionary hera advertises to
// argus via a {"type":"hotkeys",...} frame on connect and on every focus
// change (design.md D12). Operator-facing keys are flagged Bar:true to drive
// argus's context-sensitive bottom bar; the full set drives argus's help
// overlay. coordPresent controls whether the COORD pane is advertised: in
// freelance mode (D11) there is no coord, so traversal hints drop the coord
// step (RAIL advances straight to AGENT; AGENT retreats straight to RAIL).
//
// This is the same source of truth the retired bottomBarText row drove; argus
// now renders it instead of hera.
func hotkeyItems(state FocusState, coordPresent bool) []HotkeyItem {
	switch state {
	case FocusCOORD:
		// `^d`/`^r`/`^p` are RAIL-focus-only; in a pane they forward to the PTY
		// (Ctrl-D/R/P), so they are NOT advertised here.
		return []HotkeyItem{
			{Key: "keys", Label: "coord PTY", Bar: true},
			{Key: "^Z", Label: "fullscreen", Bar: true},
			{Key: "^→", Label: "agent", Bar: true},
			{Key: "^←", Label: "rail", Bar: true},
			{Key: "^Q", Label: "rail", Bar: true},
		}
	case FocusAGENT:
		if !coordPresent {
			return []HotkeyItem{
				{Key: "keys", Label: "agent PTY", Bar: true},
				{Key: "^Z", Label: "fullscreen", Bar: true},
				{Key: "^←", Label: "rail", Bar: true},
				{Key: "^Q", Label: "rail", Bar: true},
			}
		}
		return []HotkeyItem{
			{Key: "keys", Label: "agent PTY", Bar: true},
			{Key: "^Z", Label: "fullscreen", Bar: true},
			{Key: "^←", Label: "coord", Bar: true},
			{Key: "^Q", Label: "rail", Bar: true},
		}
	default: // FocusRAIL
		advance := HotkeyItem{Key: "^→", Label: "coord", Bar: true}
		if !coordPresent {
			advance = HotkeyItem{Key: "^→", Label: "agent", Bar: true}
		}
		return []HotkeyItem{
			{Key: "j/k", Label: "move", Bar: true},
			{Key: "Enter", Label: "agent", Bar: true},
			advance,
			{Key: "n", Label: "new", Bar: true},
			// `w` spawns a worker under the selected coordinator (RAIL-focus-only,
			// applies on a coordinator-resolvable row — orchestrator header,
			// sub-coordinator row, or a leaf agent attached to a coord). `J` adopts
			// the selected freelancer into a coordinator (RAIL-focus-only, applies
			// only on a freelance row). Both are advertised on the bottom bar so
			// they are discoverable; the per-row applicability is surfaced by the
			// op itself (a visible "not applicable" notice on a wrong row), matching
			// how the other rail mutation keys (n/r/a) are always listed (BUG-007).
			// J is placed immediately after w (both are "spawn/adopt" ops) so it
			// appears in the bottom bar before the rename/delete/archive block —
			// which are pushed past the bar's visible width at typical terminals
			// (BUG-031).
			{Key: "w", Label: "new agent", Bar: true},
			{Key: "J", Label: "adopt", Bar: true},
			{Key: "r", Label: "rename", Bar: true},
			{Key: "^d", Label: "del", Bar: true},
			{Key: "a", Label: "archive", Bar: true},
			{Key: "←", Label: "parent", Bar: true},
			{Key: "s", Label: "status+", Bar: true},
			{Key: "S", Label: "status-", Bar: true},
			{Key: "/", Label: "search", Bar: true},
			{Key: "?", Label: "help", Bar: true},
			// Help-overlay-only rail keys (Bar:false): kept off the bottom bar.
			{Key: "l", Label: "listall", Bar: false},
			{Key: "^r", Label: "prune", Bar: false},
			{Key: "^p", Label: "PR", Bar: false},
			{Key: "Esc", Label: "argus", Bar: true},
		}
	}
}
