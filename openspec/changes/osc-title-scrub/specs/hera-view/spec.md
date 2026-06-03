# hera-view — delta for osc-title-scrub

## ADDED Requirements

### Requirement: Pane byte streams are scrubbed of OSC sequences before emulation

The system SHALL remove every OSC sequence (`ESC ]` … terminator, any OSC code: 0/1/2/8/…) from a pane's byte stream BEFORE the bytes reach the pane's terminal emulator, so an OSC payload (e.g. a Claude Code set-title string) is never painted as printable text in the pane. The scrub MUST apply to the FULL stream a pane ingests: the ring snapshot delivered at bind time AND every subsequent live byte chunk.

The scrubber MUST recognize BOTH OSC terminators — BEL (`0x07`) and 7-bit ST (`ESC \`) — and MUST drop the terminator together with the payload. CAN (`0x18`) and SUB (`0x1a`) cancel an in-flight OSC; an ESC that begins a new sequence inside an OSC payload cancels the OSC and the new sequence MUST be processed normally. A 0x9C byte inside an OSC payload MUST NOT be treated as a terminator (it may be a UTF-8 continuation byte — e.g. in Claude's "✳" spinner title — and treating it as C1 ST is the upstream parser bug that leaks the payload).

The scrubber MUST be stateful across chunk boundaries: an OSC sequence split across two or more chunks (including the snapshot→live boundary, and a terminator's `ESC` and `\` arriving in different chunks) MUST still be removed in full. A lone trailing `ESC` at the end of a chunk MUST be held until the next chunk decides whether it introduces an OSC.

Non-OSC escape sequences (CSI colors, cursor movement, SGR, etc.) and all ordinary printable bytes MUST pass through byte-for-byte unchanged. The scrub MUST be bounded: a pathological OSC that never terminates MUST stop being dropped after a fixed cap so the filter can never swallow a stream unboundedly.

#### Scenario: Inline OSC title with BEL terminator is not painted

- **WHEN** a pane's stream contains `before` + `ESC ] 0 ; My Title BEL` + `after` within a single chunk
- **THEN** the bytes delivered to the pane's emulator MUST be exactly `beforeafter` — no byte of the OSC introducer, payload, or BEL terminator may reach the emulator

#### Scenario: OSC with ST terminator is removed

- **WHEN** a pane's stream contains `ESC ] 2 ; My Title ESC \` between ordinary text
- **THEN** the entire sequence including the two-byte ST terminator MUST be removed and the surrounding text delivered unchanged

#### Scenario: OSC split across chunk boundaries is removed

- **WHEN** an OSC sequence is split across two chunks (e.g. chunk 1 ends mid-payload, or chunk 1 ends with the terminator's `ESC` and chunk 2 begins with `\`), including the case where chunk 1 is the bind-time snapshot and chunk 2 is the first live chunk
- **THEN** no byte of the sequence may reach the emulator, and bytes after the terminator MUST be delivered unchanged

#### Scenario: Non-OSC escape sequences survive the scrub

- **WHEN** a pane's stream contains SGR color codes and cursor-movement CSI sequences (e.g. `ESC [ 3 1 m`, `ESC [ 6 G`) around an OSC title sequence
- **THEN** every non-OSC escape sequence MUST be delivered to the emulator byte-for-byte unchanged while the OSC sequence is removed

#### Scenario: 0x9C inside an OSC payload does not terminate it

- **WHEN** an OSC payload contains a multi-byte UTF-8 glyph whose encoding includes a `0x9C` byte (e.g. `✳` = `E2 9C B3`), followed by more payload and then a BEL
- **THEN** the scrub MUST continue dropping through the `0x9C` and terminate only at the BEL, so no payload byte after the `0x9C` leaks to the emulator
