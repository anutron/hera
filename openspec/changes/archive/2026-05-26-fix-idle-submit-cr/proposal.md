## Why

The idle-submit path was specced and implemented to terminate the injected body with LF (`\n`), on the assumption that LF is "the newline" that Claude Code's TUI treats as Enter. That assumption is wrong. The recipient PTY runs in raw mode, so termios does NOT translate CR to LF — the byte the keyboard's Return key actually emits (and the only byte Claude Code's input handler treats as submit) is CR (`\r`, byte 0x0D). Today's live confirmation: hera writes the body, marks it `delivery_mode=idle_submit`, the formatted body lands as visible text in the recipient's input buffer, but the agent never receives a submit — the operator still has to press Enter manually. Auto-inject as specified is silently a busy-buffer in practice.

## What Changes

This is a one-byte wire fix. The idle-path injector writes CR instead of LF; the spec text and tests change to match. The busy-buffer path is unchanged (it writes no terminator and that remains correct).

- Change `internal/inject/inject.go` to write `formatted+"\r"` on the idle branch (was `formatted+"\n"`).
- Update the spec scenarios that assert the trailing byte from `\n` to `\r`, with a short WHY (raw-mode PTY, no CR→LF translation, CR is what Enter sends).
- Update `internal/inject/inject_test.go` idle-path assertions from `body+"\n"` to `body+"\r"`. Busy-buffer assertions are unchanged.

**Out of scope:**

- Busy-buffer path. No terminator is correct there.
- `FormatBody` and the `[hera from <role>] <body>` prefix.
- Debounce, idle tracker, settings handler — none touch the wire byte.
- Writing `\r\n`. The TUI only needs CR; writing LF after CR would land as a visible newline character in the input buffer.

## Capabilities

### Modified Capabilities

- `hera-coordination`: Corrects the idle-submit terminator byte from LF to CR in the "Messages auto-submitted when recipient is idle" and "Auto-inject master switch" requirements.

### New Capabilities

None.

## Impact

- **No new dependencies, no new files, no schema migration.** One-byte wire change plus matching spec/test text.
- **Backwards compatible.** Persisted config rows, delivery_mode enum, and external API shapes are unchanged. Operators see no behavior difference beyond "auto-submit actually fires now."
- **Verification requires a daemon rebuild.** Unit tests prove the byte; the end-to-end "Enter actually fires in Claude Code's TUI" check requires Aaron to rebuild the daemon and run a live cross-agent send. Flagged in the handoff report.
