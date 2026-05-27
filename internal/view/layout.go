package view

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/anutron/argus-sdk/terminalpane"
)

// RailWidth is the fixed character width of the navigation rail column.
// See design.md D7.
const RailWidth = 22

// layoutPieces holds references to the individual chrome and body
// primitives composed by buildLayout. The app keeps these around to
// drive updates (rail refresh, pane swap) without recomposing the Flex
// each time.
type layoutPieces struct {
	root     *tview.Flex
	topBar   *tview.TextView
	rail     *tview.TreeView
	coord    *terminalpane.TerminalPane
	agent    *terminalpane.TerminalPane
	bottom   *tview.TextView
	body     *tview.Flex
	rootRoot *tview.TreeNode // rail's root node
}

// buildLayout composes the tview Flex tree described in design.md D7:
//
//	Flex (rows):
//	  TopBar       (height 1, "HERA" left-aligned)
//	  Body  (cols):
//	    Rail       (width RailWidth)
//	    CoordPane  (flex 1)
//	    AgentPane  (flex 1)
//	  BottomBar    (height 1, key hints)
//
// Coord and agent panes split the remaining horizontal space evenly.
// The TopBar contains the literal text "HERA"; the BottomBar carries
// focus-state-aware hints (placeholder text in Stage F; Stage G/H wires
// real focus state). The two terminalpanes own their own border and
// title rendering, so this layer only wires titles and arranges them in
// the Flex.
func buildLayout(coord, agent *terminalpane.TerminalPane) layoutPieces {
	top := tview.NewTextView()
	top.SetText("HERA")
	top.SetTextAlign(tview.AlignLeft)
	top.SetTextColor(tcell.ColorWhite)

	rootNode := tview.NewTreeNode("Projects").SetSelectable(false)
	rail := tview.NewTreeView().
		SetRoot(rootNode).
		SetCurrentNode(rootNode)
	rail.SetBorder(true)
	rail.SetTitle("Rail")
	rail.SetTopLevel(0)

	coord.SetTitle("Coord")
	agent.SetTitle("Agent")

	body := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(rail, RailWidth, 0, false).
		AddItem(coord, 0, 1, false).
		AddItem(agent, 0, 1, false)

	bottom := tview.NewTextView()
	bottom.SetText(defaultBottomBarText())
	bottom.SetTextAlign(tview.AlignLeft)
	bottom.SetTextColor(tcell.ColorWhite)

	root := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(top, 1, 0, false).
		AddItem(body, 0, 1, false).
		AddItem(bottom, 1, 0, false)

	return layoutPieces{
		root:     root,
		topBar:   top,
		rail:     rail,
		coord:    coord,
		agent:    agent,
		bottom:   bottom,
		body:     body,
		rootRoot: rootNode,
	}
}

// defaultBottomBarText returns the RAIL-focus bottom-bar hints used as a
// placeholder until Stage G/H wires a focus-state-aware bar.
func defaultBottomBarText() string {
	return "[RAIL] j/k move  Enter agent  Ctrl-→ coord  n new  r rename  ^d del  a archive  l listall  ? help"
}
