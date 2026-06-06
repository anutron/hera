package view

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/anutron/argus-sdk/theme"
	"github.com/anutron/argus-sdk/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/anutron/hera/internal/db"
)

// rosterEntry is one agent under a coordinator, carrying the same status
// inputs the rail row renders so the Details roster glyph matches the rail's
// glyph for that agent exactly.
type rosterEntry struct {
	Name       string
	Kind       string
	HasState   bool
	Status     string
	ArgusIdle  bool
	NeedsInput bool
	Live       bool
	Archived   bool
}

// coordDetails is the metadata the Details pane renders for a selected
// coordinator. It is built from the rail's orchEntry (name, coord status,
// roster — the rail's source of truth, so Details never disagrees with the
// rail) plus the hera DB (created, mission, constraints, repos, last
// activity). No values are inferred; every field is read straight from
// stored state.
type coordDetails struct {
	Name string

	// Coord status inputs — fed to the shared statusIcon at Draw time so the
	// Details status glyph matches the rail's coordinator-header glyph, and a
	// running coordinator animates the same spinner.
	HasState   bool
	Status     string
	CoordIdle  bool
	NeedsInput bool
	CoordLive  bool
	Archived   bool

	Created      time.Time
	LastActivity time.Time
	Mission      string
	Constraints  string
	Repos        []string
	Roster       []rosterEntry
}

// buildCoordDetails derives the Details metadata for the coordinator described
// by orch. Name, coord status, and the agent roster come from the rail-built
// orchEntry (the rail's source of truth — promoted sub-coordinators included);
// created time, mission, constraints, repos-in-scope, and last-agent-activity
// come from the hera DB. A sub-coordinator selection passes its childOrch here
// (itself an *orchEntry), so the same builder serves root and sub coordinators.
//
// It issues only local SQLite reads (no argus HTTP), so it is safe to call on
// the tview event loop from applyRailSelection.
func buildCoordDetails(ctx context.Context, database *db.DB, orch *orchEntry) (coordDetails, error) {
	archived := orch.Archived || orch.CoordArgusArchived
	dispStatus, dispIdle := liveCoordDisplayStatus(archived, orch.CoordStatus, orch.CoordIdle)
	cd := coordDetails{
		Name:       orch.Name,
		HasState:   orch.CoordHasState,
		Status:     dispStatus,
		CoordIdle:  dispIdle,
		NeedsInput: orch.CoordNeedsInput,
		CoordLive:  orch.CoordTaskID != "",
		Archived:   archived,
	}

	// Roster = the rail's DEFAULT-VISIBLE child rows (excludes the coord role;
	// includes promoted sub-coordinators carrying childOrch). Archived / dead /
	// argus-archived children are skipped — the rail buckets those behind the
	// Archive expando, so "the same state the rail shows" means they are hidden
	// here too. Mirror each row's status inputs so the roster glyph matches the
	// rail's row glyph.
	for _, r := range orch.Roles {
		if r == nil || r.Archived || r.Dead || r.ArgusArchived {
			continue
		}
		cd.Roster = append(cd.Roster, rosterEntry{
			Name:       r.Name,
			Kind:       r.RoleKind,
			HasState:   r.HasState,
			Status:     r.Status,
			ArgusIdle:  r.ArgusIdle,
			NeedsInput: r.NeedsInput,
			Live:       r.Live,
		})
	}

	if database == nil {
		return cd, nil
	}

	// Created + last-activity seed from the orchestrator row.
	if o, err := database.Orchestrators.GetByID(ctx, orch.ID); err == nil && o != nil {
		cd.Created = o.CreatedAt
		cd.LastActivity = o.CreatedAt
	}

	roles, err := database.Roles.ListByOrchestratorInclusive(ctx, orch.ID)
	if err != nil {
		return cd, fmt.Errorf("coord details: list roles: %w", err)
	}

	// Mission/constraints come from the coordinator role — prefer the one the
	// rail captured (CoordRoleID); else the first coordinator-kind role.
	var coordRole *db.Role
	for _, role := range roles {
		if role == nil || role.Kind != db.KindCoordinator {
			continue
		}
		if orch.CoordRoleID != 0 && role.ID == orch.CoordRoleID {
			coordRole = role
			break
		}
		if coordRole == nil {
			coordRole = role
		}
	}
	if coordRole != nil {
		cd.Mission = coordRole.Mission
		cd.Constraints = coordRole.Constraints
	}

	// Repos in scope (distinct argus projects) + last activity (max over role
	// creation, their bindings' start/end, and their status updates).
	repoSet := map[string]struct{}{}
	bump := func(t time.Time) {
		if t.After(cd.LastActivity) {
			cd.LastActivity = t
		}
	}
	for _, role := range roles {
		if role == nil {
			continue
		}
		if role.ArgusProject != "" {
			repoSet[role.ArgusProject] = struct{}{}
		}
		bump(role.CreatedAt)
		if bnds, err := database.Bindings.ListByRole(ctx, role.ID); err == nil {
			for _, b := range bnds {
				if b == nil {
					continue
				}
				bump(b.StartedAt)
				if b.EndedAt != nil {
					bump(*b.EndedAt)
				}
			}
		}
		if rs, err := database.RoleStatus.Get(ctx, role.ID); err == nil && rs != nil {
			bump(rs.UpdatedAt)
		}
	}
	repos := make([]string, 0, len(repoSet))
	for p := range repoSet {
		repos = append(repos, p)
	}
	sort.Strings(repos)
	cd.Repos = repos

	return cd, nil
}

// liveCoordDisplayStatus returns the (status, idle) pair to display for a
// coordinator header. For non-archived coordinators, argus auto-completes the
// coordinator's task when the session goes idle — but the coordinator remains
// live. Mask "complete" → in_progress+idle so the display shows ☾ (idle moon)
// rather than ✓, which would mislead the operator into thinking it is done.
// Archived coordinators are exempt: their session is genuinely finished.
func liveCoordDisplayStatus(archived bool, status string, idle bool) (string, bool) {
	if !archived && status == "complete" {
		return "in_progress", true
	}
	return status, idle
}

// statusLabel maps argus-reported task state to a short human label that
// matches the status glyph statusIcon picks. "unknown" when state is absent.
func statusLabel(hasState, needsInput bool, status string, idle bool) string {
	if !hasState {
		return "unknown"
	}
	if needsInput {
		return "needs input"
	}
	switch status {
	case "pending":
		return "pending"
	case "complete":
		return "complete"
	case "in_review":
		return "in review"
	case "in_progress":
		if idle {
			return "idle"
		}
		return "working"
	}
	return status
}

// shortKind renders a role kind for the roster. A roster "coordinator" is a
// nested sub-coordinator, labeled as such for clarity.
func shortKind(kind string) string {
	switch kind {
	case string(db.KindCoordinator):
		return "sub-coord"
	case string(db.KindWorker):
		return "worker"
	case string(db.KindFreelance):
		return "freelance"
	}
	return kind
}

// fmtDetailTime formats a timestamp for the Details pane, or an en-dash
// placeholder when zero.
func fmtDetailTime(t time.Time) string {
	if t.IsZero() {
		return "–"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// wrapText word-wraps s to width columns, preserving explicit newlines as
// paragraph breaks. Words longer than width are emitted whole (DrawText clips
// them at the pane edge). Byte-length based — adequate for the prose fields.
func wrapText(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			continue
		}
		cur := ""
		for _, wd := range words {
			switch {
			case cur == "":
				cur = wd
			case len(cur)+1+len(wd) <= width:
				cur += " " + wd
			default:
				lines = append(lines, cur)
				cur = wd
			}
		}
		if cur != "" {
			lines = append(lines, cur)
		}
	}
	return lines
}

// detailsPane is the right-side coordinator Details column (D-style, modeled on
// argus's Details panel). It is a tview.Box subclass that renders a coordDetails
// as labeled fields with the argus-sdk theme, reusing the rail's shared
// statusIcon for status glyphs. It is not part of the focus ladder, so its
// border stays the unfocused color.
type detailsPane struct {
	*tview.Box
	data    coordDetails
	hasData bool
	// now is injectable for tests; defaults to time.Now. Drives the spinner
	// frame so a running coordinator/agent animates.
	now func() time.Time
}

// newDetailsPane constructs an empty Details pane with a border + title.
func newDetailsPane() *detailsPane {
	d := &detailsPane{Box: tview.NewBox()}
	d.SetBorder(true)
	d.SetTitle(" Details ")
	d.SetBorderColor(theme.ColorBorder)
	d.SetTitleColor(theme.ColorTitle)
	return d
}

// SetDetails replaces the rendered metadata. Runs on the tview event loop.
func (d *detailsPane) SetDetails(cd coordDetails) {
	d.data = cd
	d.hasData = true
}

func (d *detailsPane) frame() int {
	now := time.Now
	if d.now != nil {
		now = d.now
	}
	return int(now().UnixMilli() / spinnerInterval.Milliseconds())
}

// Draw renders the bordered box then the labeled metadata fields.
func (d *detailsPane) Draw(screen tcell.Screen) {
	d.DrawForSubclass(screen, d)
	x, y, w, h := d.GetInnerRect()
	if w <= 0 || h <= 0 {
		return
	}
	if !d.hasData {
		widget.DrawText(screen, x, y, w, "(no coordinator selected)", theme.StyleDimmed)
		return
	}
	cd := d.data
	frame := d.frame()

	row := 0
	emit := func(s string, style tcell.Style) {
		if row < h {
			widget.DrawText(screen, x, y+row, w, s, style)
		}
		row++
	}
	blank := func() { row++ }
	field := func(label, value string, valStyle tcell.Style) {
		if row < h {
			lbl := label + ": "
			widget.DrawText(screen, x, y+row, w, lbl, theme.StyleDimmed)
			if n := runeLen(lbl); n < w {
				widget.DrawText(screen, x+n, y+row, w-n, value, valStyle)
			}
		}
		row++
	}
	// glyphLine draws an indented "<glyph> <text> <suffix>" row reusing
	// statusIcon for the glyph + its style.
	glyphLine := func(indent int, archived, hasState, needsInput bool, status string, idle, live bool, text, suffix string) {
		if row < h {
			col := x + indent
			glyph, gstyle := statusIcon(archived, hasState, needsInput, status, idle, live, frame)
			if col < x+w {
				screen.SetContent(col, y+row, glyph, nil, gstyle)
				col += 2
			}
			if col < x+w && text != "" {
				widget.DrawText(screen, col, y+row, x+w-col, text, gstyle)
				col += runeLen(text) + 1
			}
			if col < x+w && suffix != "" {
				widget.DrawText(screen, col, y+row, x+w-col, suffix, theme.StyleDimmed)
			}
		}
		row++
	}
	section := func(text string) {
		if strings.TrimSpace(text) == "" {
			emit("  (none)", theme.StyleDimmed)
			return
		}
		for _, ln := range wrapText(text, w-2) {
			emit("  "+ln, theme.StyleNormal)
		}
	}

	// Name (title) + blank.
	emit(cd.Name, theme.StyleTitle)
	blank()

	// Status: <glyph> <label> — reuse the coordinator-header glyph + style.
	if row < h {
		lbl := "Status: "
		widget.DrawText(screen, x, y+row, w, lbl, theme.StyleDimmed)
		col := x + runeLen(lbl)
		glyph, gstyle := statusIcon(cd.Archived, cd.HasState, cd.NeedsInput, cd.Status, cd.CoordIdle, cd.CoordLive, frame)
		if col < x+w {
			screen.SetContent(col, y+row, glyph, nil, gstyle)
			col += 2
		}
		if col < x+w {
			widget.DrawText(screen, col, y+row, x+w-col, statusLabel(cd.HasState, cd.NeedsInput, cd.Status, cd.CoordIdle), gstyle)
		}
	}
	row++
	field("Created", fmtDetailTime(cd.Created), theme.StyleNormal)
	field("Last activity", fmtDetailTime(cd.LastActivity), theme.StyleNormal)
	blank()

	// Repos in scope.
	emit("Repos in scope:", theme.StyleDimmed)
	if len(cd.Repos) == 0 {
		emit("  (none)", theme.StyleDimmed)
	} else {
		for _, r := range cd.Repos {
			emit("  • "+r, theme.StyleNormal)
		}
	}
	blank()

	// Mission + Constraints (wrapped).
	emit("Mission:", theme.StyleDimmed)
	section(cd.Mission)
	blank()
	emit("Constraints:", theme.StyleDimmed)
	section(cd.Constraints)
	blank()

	// Agent roster.
	emit(fmt.Sprintf("Agents (%d):", len(cd.Roster)), theme.StyleDimmed)
	if len(cd.Roster) == 0 {
		emit("  (none)", theme.StyleDimmed)
	} else {
		for _, re := range cd.Roster {
			glyphLine(2, re.Archived, re.HasState, re.NeedsInput, re.Status, re.ArgusIdle, re.Live, re.Name, "("+shortKind(re.Kind)+")")
		}
	}
	blank()

	// TODO(coord-details): the inferred living-summary (description / goal /
	// scope) is not implemented yet — this is the reserved placeholder.
	emit("Summary:", theme.StyleDimmed)
	emit("  (auto-generated overview coming soon)", theme.StyleDimmed)
}
