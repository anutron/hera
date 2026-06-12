## ADDED Requirements

### Requirement: In-progress pane input survives navigation AND resize for every pane slot

The view application MUST NOT echo typed input locally — typed bytes appear in a pane only via the PTY echo that arrives on the live byte channel, never as part of the ring-buffer snapshot. Therefore in-progress, unsubmitted input lives ONLY in the live emulator that consumed it. Reconstructing a pane by replaying the (possibly truncated) ring-buffer snapshot through a FRESH emulator races the in-flight live frame and can drop the in-progress input line.

In-progress, unsubmitted input typed into ANY pane — a worker/agent pane, a root-orchestrator coordinator pane, OR a sub-coordinator pane — MUST survive both of the following without loss:

- **Navigating away to another row and back.** The system MUST park the live pane on nav-away (keyed by argus task id, for both the AGENT and COORD slots) and restore the SAME live emulator on nav-back, rather than tearing it down and rebuilding from a snapshot. The parked-pane caches MUST be bounded (least-recently-parked eviction) so a long session cannot accumulate unbounded live off-screen emulators.

- **A change in the pane's on-screen dimensions (resize reflow).** When a pane's inner rect changes — including the full-width ↔ split-width transition a coordinator pane undergoes when the operator drills from a coordinator into one of its own workers and back — the system MUST resize the EXISTING live emulator in place rather than rebuilding it from the ring-buffer snapshot. The active screen reflows to the new width via the in-place resize; scrollback history MAY retain its prior wrapping. Input preservation takes precedence over reflowing scrollback history.

The fresh-subscription rebuild (which discards the live emulator) MUST remain confined to the REATTACH path, where the dead session's ring buffer has already been explicitly discarded and there is no in-progress input to protect.

#### Scenario: Coordinator input survives nav-away-and-back

- **WHEN** the operator types unsubmitted input into a live coordinator pane (root orchestrator coord or sub-coordinator), navigates the rail to another coordinator, then navigates back
- **THEN** the coordinator pane MUST still show the in-progress input on return (its parked live emulator is restored, not rebuilt from the input-less snapshot)

#### Scenario: Coordinator input survives the full-width-to-split resize

- **WHEN** the operator types unsubmitted input into a full-width coordinator pane and then drills into one of that coordinator's own workers, shrinking the coord pane from full-width to the coord+agent split
- **THEN** the resize-triggered reflow MUST resize the live coordinator emulator in place and MUST preserve the in-progress input — it MUST NOT rebuild the emulator from the ring snapshot

#### Scenario: Agent input survives a resize reflow

- **WHEN** a live agent pane carrying in-progress unsubmitted input has its on-screen dimensions changed
- **THEN** the reflow MUST resize the live agent emulator in place and preserve the input

#### Scenario: Reattach still rebuilds from a fresh subscription

- **WHEN** a dead session is restarted and the reattach path clears the splash after discarding the old session's ring buffer (`ResetSubscription`)
- **THEN** the pane MUST be rebuilt from a fresh subscription at the correct dimensions (no in-progress input exists to preserve on this path)
