# hera-view delta: pane fullscreen (BUG-027)

## ADDED Requirements

### Requirement: Ctrl-Z toggles pane fullscreen

When focus is on the COORD or AGENT pane, pressing Ctrl-Z SHALL maximize that pane to full screen:
the rail and the other pane are hidden, and the selected pane fills the entire body area.
Pressing Ctrl-Z again while in fullscreen SHALL exit fullscreen and restore the normal
rail + coord + agent split layout.

Ctrl-Z while focus is on the RAIL SHALL be consumed as a no-op (never forwarded as the
SIGTSTP byte 0x1a to any PTY or widget).

#### Scenario: Ctrl-Z in COORD enters fullscreen for COORD

- **GIVEN** focus is on the COORD pane and fullscreen is inactive
- **WHEN** the operator presses Ctrl-Z
- **THEN** fullscreen becomes active showing the COORD pane only (rail and AGENT pane hidden)
- **AND** the top indicator bar shows "Coord"

#### Scenario: Ctrl-Z in AGENT enters fullscreen for AGENT

- **GIVEN** focus is on the AGENT pane and fullscreen is inactive
- **WHEN** the operator presses Ctrl-Z
- **THEN** fullscreen becomes active showing the AGENT pane only (rail and COORD pane hidden)
- **AND** the top indicator bar shows "Agent"

#### Scenario: Ctrl-Z exits fullscreen

- **GIVEN** fullscreen is active on any pane
- **WHEN** the operator presses Ctrl-Z
- **THEN** fullscreen becomes inactive and the normal split layout (rail + coord + agent) is restored
- **AND** the top indicator bar is cleared

#### Scenario: Ctrl-Z in RAIL is consumed, does nothing

- **GIVEN** focus is on the RAIL
- **WHEN** the operator presses Ctrl-Z
- **THEN** the event is consumed (not forwarded); no fullscreen state change occurs

### Requirement: Focus ladder operates in fullscreen

While fullscreen is active, the existing Ctrl-Left / Ctrl-Right focus-ladder keys SHALL
navigate between panes while staying in fullscreen, mirroring the normal Rail → Coord → Agent
ladder semantics:

- Fullscreen AGENT + Ctrl-Left → switch to fullscreen COORD (stay fullscreen)
- Fullscreen COORD + Ctrl-Right → switch to fullscreen AGENT (stay fullscreen)
- Fullscreen COORD + Ctrl-Left → exit fullscreen and move focus to RAIL (normal split restored)
- Fullscreen AGENT + Ctrl-Right → no-op (AGENT is the rightmost pane; already at the end)

Ctrl-Q while fullscreen is active SHALL exit fullscreen and return focus to RAIL
(normal split restored), consistent with its always-go-to-RAIL semantics.

#### Scenario: Ctrl-Right in fullscreen COORD switches to fullscreen AGENT

- **GIVEN** fullscreen is active on COORD
- **WHEN** the operator presses Ctrl-Right
- **THEN** fullscreen remains active and the AGENT pane is shown instead of COORD
- **AND** focus moves to AGENT

#### Scenario: Ctrl-Left in fullscreen AGENT switches to fullscreen COORD

- **GIVEN** fullscreen is active on AGENT
- **WHEN** the operator presses Ctrl-Left
- **THEN** fullscreen remains active and the COORD pane is shown instead of AGENT
- **AND** focus moves to COORD

#### Scenario: Ctrl-Left in fullscreen COORD exits fullscreen to RAIL

- **GIVEN** fullscreen is active on COORD
- **WHEN** the operator presses Ctrl-Left
- **THEN** fullscreen becomes inactive; the normal split layout is restored
- **AND** focus moves to RAIL

#### Scenario: Ctrl-Right in fullscreen AGENT is a no-op

- **GIVEN** fullscreen is active on AGENT
- **WHEN** the operator presses Ctrl-Right
- **THEN** fullscreen remains active on AGENT; no state change occurs

### Requirement: Fullscreen indicator in top bar

While fullscreen is active, the top-bar row (one character tall, currently blank) SHALL show
a label naming the visible pane ("Coord" or "Agent"), so the operator always knows which
pane is maximized. The indicator MUST be cleared when fullscreen is exited.

#### Scenario: Top bar shows pane name in fullscreen

- **GIVEN** fullscreen is inactive (top bar is blank)
- **WHEN** the operator enters fullscreen on any pane
- **THEN** the top bar displays the name of the fullscreen pane ("Coord" or "Agent")

#### Scenario: Top bar cleared on fullscreen exit

- **GIVEN** fullscreen is active and the top bar shows a pane name
- **WHEN** the operator exits fullscreen (via Ctrl-Z, Ctrl-Q, or Ctrl-Left from COORD fullscreen)
- **THEN** the top bar is cleared (blank again)
