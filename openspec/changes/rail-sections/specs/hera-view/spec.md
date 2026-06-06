## ADDED Requirements

### Requirement: Pinned section at the rail top with a `P` toggle

The system SHALL render a `Pinned` section at the TOP of the rail, above the orchestrator list, mirroring argus's task-panel Pinned section. The section is introduced by a `Pinned` separator that is rendered ONLY when at least one pinned coordinator or pinned agent exists (so the operator never lands on an empty section). Pinned items SHALL be fully reachable by normal j/k navigation.

A coordinator (orchestrator header) or an agent (a worker or sub-coordinator role) is PINNED when its hera row carries a `pinned_at` timestamp. A pinned coordinator SHALL render in the Pinned section as a foldable coordinator row carrying its subtree (its children render nested beneath it as usual), and MUST NOT also render in the active orchestrator tree. A pinned agent SHALL float OUT of its coordinator's child list and render in the Pinned section as a standalone row, and MUST NOT be counted in its (former) coordinator's live-child `(N)` count. Unpinning a row SHALL return it to its normal place on the next rail rebuild.

The system SHALL bind `P` (focus `RAIL`) to toggle the pinned state of the selected coordinator or agent: pin when the row is not pinned, unpin when it is. Pin and archive SHALL be MUTUALLY EXCLUSIVE, mirroring argus's `SetPinned`/`SetArchived`: pinning a row MUST clear its archived state (its hera `archived_at`, and when its bound argus task is argus-archived, the argus side too via the unarchive endpoint), and archiving a row MUST clear its `pinned_at`. Pinned state takes PRECEDENCE over archived state in rendering — a row can never be in both the Pinned and an Archive section.

Pin state SHALL be persisted HERA-SIDE in a nullable `pinned_at` column on `orchestrators` and `roles` (mirroring `archived_at`), because argus exposes no pin/SetPinned REST endpoint. Consequently a pinned hera coordinator/agent does NOT reflect into argus's own TUI Pinned section. `P` against a freelance row (an unmanaged argus task with no hera role) is NOT applicable and MUST give visible feedback naming the gap (pinning is hera-side; a freelancer has no hera row to persist a pin on), never a silent no-op.

#### Scenario: Pinned coordinator floats to the Pinned section

- **WHEN** an orchestrator's `pinned_at` is set
- **THEN** a `Pinned` separator MUST render at the top of the rail AND the orchestrator MUST render as a foldable coordinator row under it (with its subtree) AND MUST NOT also render in the active orchestrator tree

#### Scenario: Pinned agent floats out of its coordinator

- **WHEN** a worker role under coordinator `foo` has its `pinned_at` set
- **THEN** the worker MUST render as a standalone row in the Pinned section AND MUST NOT appear among `foo`'s active children AND MUST NOT be counted in `foo`'s live-child `(N)` count

#### Scenario: `P` pins the selected row and clears archived

- **WHEN** focus is `RAIL` and the operator presses `P` against a non-pinned, archived role
- **THEN** hera MUST set the role's `pinned_at`, clear its `archived_at`, and (when its bound argus task is argus-archived) issue the argus unarchive endpoint, so the row moves into the Pinned section and out of any Archive section

#### Scenario: `P` again unpins

- **WHEN** focus is `RAIL` and the operator presses `P` against a pinned coordinator
- **THEN** hera MUST clear its `pinned_at`, so it returns to the active orchestrator tree on the next rebuild

#### Scenario: Archiving a pinned row clears the pin

- **WHEN** a pinned row is archived (via `a`)
- **THEN** the row's `pinned_at` MUST be cleared so it leaves the Pinned section, AND it MUST render in its Archive section per the archive rules

#### Scenario: `P` on a freelancer pins it at root level (BUG-024)

- **WHEN** focus is `RAIL` and the operator presses `P` against a freelance row
- **THEN** hera MUST toggle the freelancer's pinned state in the rail's in-memory `pinnedFreelance` map (persisted via `railViewState` to the config table), AND the pinned freelancer MUST float to the ROOT level of the Pinned block (depth 0, intermixed with pinned coordinators, no ancestry shown), AND MUST NOT also render in the Freelance section (no double-render), AND MUST NOT write any hera role DB row or call argus. Pressing `P` again MUST unpin and return the freelancer to the Freelance section.

#### Scenario: Every freelance row carries the (F) marker (BUG-024)

- **WHEN** a freelance task renders in the Freelance section OR in the Pinned block
- **THEN** the row MUST display the `iconFreelance` marker (`nf-md-alpha_f_box`, U+F0229) after the status icon, so freelancers are visually distinct from managed agents and coordinators at a glance. The marker MUST render consistently at the same icon-column position in both sections.

#### Scenario: Pinned managed sub-item renders as a stacked two-line breadcrumb (BUG-025)

- **WHEN** a pinned managed role (worker or sub-coordinator) that lives INSIDE a coordinator floats to the Pinned block
- **THEN** its entry in the Pinned block MUST span TWO consecutive visual lines:
  - **Line 1** (`railRowPinnedBreadcrumb`, selectable cursor target, DIMMED): the role's status icon followed by the full ancestry trail from the root coordinator down to the role's immediate parent, each name separated by ` › ` and ending with a trailing ` › ` (e.g. `○ kbtest › nested-sub › `). This line is the cursor target for j/k and pane-binding; `currentRef()` MUST return the role, identical to selecting the name line.
  - **Line 2** (`railRowRole` with `isBreadcrumbContinuation=true`, NON-selectable): the role's name in full-bright style (`StyleNormal`, or `StyleSelected` when line 1 is the cursor), right-aligned age, indented one `indentStep` more than line 1. This line MUST NOT be individually selectable; j/k MUST treat both lines as a single navigation unit (pressing j once from the entry before lands on line 1; pressing j once from line 1 skips line 2 and lands on the next selectable row after both lines).
- A **top-level pinned coordinator** (a pinned `orchEntry`) MUST stay single-line (the existing `railRowOrch` render path).
- A **pinned freelancer** MUST stay single-line (rendered at root depth with no coordinator ancestry per BUG-024).
- When the ancestry trail is too wide for the available line width, it MUST be **left-truncated** with a leading `…` so the nearest parent (rightmost text) remains visible. The role name on line 2 MUST NEVER truncate due to ancestry overflow.
- `SelectByRoleID` and `SelectByArgusTaskID` MUST find `railRowPinnedBreadcrumb` rows (the cursor target) in addition to non-continuation `railRowRole` rows.

#### Scenario: Deep ancestry chain left-truncates in breadcrumb line

- **WHEN** the breadcrumb ancestry trail (e.g. `grandparent › parent › child › `) exceeds the available line-1 width
- **THEN** the leftmost ancestors MUST be dropped and the trail replaced with `…` + the remaining suffix, keeping the nearest parent visible. The role name on line 2 MUST render in full with no truncation from the ancestry.

### Requirement: Archived Hera tasks render in a navigable Archive section below Freelance

The system SHALL render a bottom-of-rail `Archive (N)` section, below the Freelance section, holding archived Hera tasks that would otherwise vanish from the rail — archived freelancers (unmanaged argus tasks the operator archived with `a`) and archived root coordinators. This Archive section MUST be reachable by normal j/k navigation WITHOUT pressing `l`, and MUST be collapsed by default; `space` or `Enter` on its header toggles its fold. `N` is the count of archived items it holds.

Archived freelancers SHALL render ONLY inside this Archive section (as standalone rows), and MUST NOT render inline within their Freelance repo group — so there is NO double-render. A Freelance repo group's live-count and visibility MUST exclude archived tasks; a repo group whose only tasks are archived MUST NOT render an empty inline group. Pressing `a` on an archived freelancer row in the Archive section MUST unarchive it (issue the argus unarchive endpoint against its task id), returning it to its Freelance repo group on the next rebuild.

The `l` listall convenience MUST continue to work: it force-expands every Archive expando (the per-coordinator expandos AND this bottom Archive section) and reveals dead rows, but it is NOT required to make this Archive section reachable — the section and its fold are reachable in the default view.

#### Scenario: Archived freelancer renders in the bottom Archive, not vanished

- **WHEN** the operator presses `a` on a freelancer and its argus task becomes archived
- **THEN** in the default view (without `l`) the rail MUST render the bottom `Archive (N)` section counting that freelancer, AND folding it open MUST reveal the freelancer as a selectable row

#### Scenario: Archived freelancer does not double-render

- **WHEN** a freelancer is archived
- **THEN** it MUST NOT appear inline in its Freelance repo group (in either the default or the `l` view) — it renders only inside the bottom Archive section

#### Scenario: `a` on an archived freelancer in the Archive section unarchives it

- **WHEN** the operator presses `a` against an archived freelancer row inside the bottom Archive section, whose argus task is `T9`
- **THEN** hera MUST issue the argus unarchive endpoint for `T9` with no hera DB write, so the task returns to its Freelance repo group on the next rebuild

#### Scenario: Archived root coordinator reachable without `l`

- **WHEN** a root coordinator is archived
- **THEN** in the default view (without `l`) the rail MUST render it inside the bottom `Archive (N)` section (collapsed by default), reachable by j/k

#### Scenario: `l` force-expands the bottom Archive section

- **WHEN** the operator presses `l`
- **THEN** the bottom `Archive (N)` section MUST be force-expanded along with every per-coordinator Archive expando, AND dead rows MUST be revealed

## MODIFIED Requirements

### Requirement: Mutation keys are RAIL-focus-only

The system SHALL recognize the RAIL-only key set (`n`, `w`, `r`, `a`, `l`, `?`, `s`, `S`, `P`, `^d`, `^r`, `^p`) ONLY when focus is `RAIL`. When focus is `COORD` or `AGENT`, every one of these keys — including the destructive/external verbs `^d`, `^r`, and `^p` — MUST be treated as ordinary input and forwarded to the bound task's PTY (per the keystroke-forwarding requirement): a printable key forwards its byte (`P` forwards the byte `P`), and `^d`/`^r`/`^p` forward their control bytes (Ctrl-D=0x04, Ctrl-R=0x12, Ctrl-P=0x10) so an agent gets EOF / reverse-search / history-prev normally. None of these keys fires a mutation or is intercepted while focus is in a pane.

#### Scenario: `n` in RAIL focus opens new-project modal

- **WHEN** focus is `RAIL` and the operator presses `n`
- **THEN** the view MUST open the new-project input modal

#### Scenario: `n` in COORD focus types into the PTY

- **WHEN** focus is `COORD` and the operator presses `n`
- **THEN** the daemon MUST POST the byte `n` to the COORD task's input endpoint AND MUST NOT open the new-project modal

#### Scenario: `r` in AGENT focus types into the PTY

- **WHEN** focus is `AGENT` and the operator presses `r`
- **THEN** the daemon MUST POST the byte `r` to the AGENT task's input endpoint AND MUST NOT open the rename modal

#### Scenario: `?` in AGENT focus types into the PTY

- **WHEN** focus is `AGENT` and the operator presses `?`
- **THEN** the daemon MUST POST the byte `?` to the AGENT task's input endpoint AND MUST NOT open the help modal

#### Scenario: `P` in RAIL focus toggles pin

- **WHEN** focus is `RAIL` and the operator presses `P` against a coordinator or agent row
- **THEN** the view MUST toggle that row's pinned state AND MUST NOT forward a byte to any PTY

#### Scenario: `P` in AGENT focus types into the PTY

- **WHEN** focus is `AGENT` and the operator presses `P`
- **THEN** the daemon MUST POST the byte `P` to the AGENT task's input endpoint AND MUST NOT toggle any pin

#### Scenario: `^d` in AGENT focus forwards Ctrl-D to the PTY

- **WHEN** focus is `AGENT` and the operator presses `^d`
- **THEN** the daemon MUST forward the control byte Ctrl-D (`0x04`) to the AGENT task's input endpoint AND MUST NOT open the delete confirm modal
