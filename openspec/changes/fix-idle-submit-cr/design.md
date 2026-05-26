## Context

Hera's idle-path injector at `internal/inject/inject.go:64` writes the formatted body terminated by LF (`\n`, byte 0x0A) to the recipient's argus task PTY via `POST /api/tasks/{id}/input`. The intent was "auto-submit on idle"; the spec uses the words "trailing newline causes Claude Code's input handler to submit the buffer immediately."

Live verification today (Aaron, on the active hera+argus daemon): cross-agent sends targeting an idle recipient report `delivery_mode=idle_submit`, the formatted body arrives in the recipient agent's input buffer with a visible newline at the end, and the recipient sits there waiting. Manual Enter is required to submit. Auto-inject is silently degrading to busy-buffer-with-extra-byte.

## Diagnosis

The keyboard's Return key emits CR (`\r`, byte 0x0D), not LF. On a normal cooked-mode TTY, the terminal driver's termios `ICRNL` flag translates CR to LF on input, so userspace programs see LF — but Claude Code's TUI puts the PTY into raw mode (no termios translation), and at that layer the submit-trigger byte is the literal CR that the keypress generates. LF is whatever the program decides it means; for Claude Code's input handler, it means "insert a newline character into the input buffer" — exactly what we observe.

The fix is to write the byte the recipient actually expects: a single CR. Not `\r\n` — appending LF after CR would land an extra visible newline in the input buffer once the buffer is submitted. Just CR.

## Decisions

### D1: Write `\r`, not `\r\n` or `\n\r`

- **Decision:** The idle path writes `formatted + "\r"`. One byte.
- **Why:** CR alone is what the Enter key emits in raw-mode PTYs. LF is interpreted by Claude Code's input handler as "newline character in the buffer" — visible, not a submit. `\r\n` would submit (the CR), and then the LF lands in whatever buffer is next — usually the agent's next prompt, as a stray newline character. Single CR avoids that.
- **Alternative considered:** Writing both `\r\n` to be defensive. Rejected — the LF has no effect on submit (CR already triggered it) and is a visible artifact in some downstream buffer.

### D2: Spec text changes from `\n` to `\r` with a short WHY

- **Decision:** Edit the existing "Messages auto-submitted when recipient is idle" requirement and the "Auto-inject master switch" requirement so their literal byte references read `\r`. Add a one-line note in the requirement body that explains "raw-mode PTY, CR is what the Return key emits."
- **Why:** The spec said `\n` because the author (and the agent that wrote it) reached for "newline = Enter" without checking raw-mode termios behavior. Future readers need the WHY or this will get re-introduced.

### D3: Busy-buffer path untouched

- **Decision:** The busy-buffer path writes the formatted body with no trailing terminator. Unchanged.
- **Why:** The user is meant to read it and press Enter themselves. Any terminator (CR or LF) defeats that. The existing assertion in `TestInject_BusyBuffersWithoutNewline` already encodes this; we keep it green.

## Risks / Trade-offs

- **Risk: Claude Code's TUI ever changes to require LF.** Negligible. Raw-mode PTYs treating CR as Enter is decades-old convention (it's what every terminal emulator on macOS and Linux does). If Claude Code ever runs in cooked mode, the termios layer would translate it back anyway.
- **Risk: Other downstream agents listening on hera-injected PTYs that DO expect LF.** Out of scope for v1 — hera's only target is Claude Code TUI sessions inside argus tasks.

## Migration

None. No DB migration, no settings migration. Persisted config rows are untouched. The next time a daemon built from this change handles an idle-submit, the wire byte is CR. There is no on-disk artifact carrying the old behavior.
