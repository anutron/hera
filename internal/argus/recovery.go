package argus

import (
	"context"
	"fmt"
	"log/slog"
)

// Reregistrar is the minimum surface Recover needs from a registrar:
// the ability to POST every registration immediately, bypassing any
// scheduled heartbeat. Both *mcp.Registrar and *settings.Registrar
// satisfy this interface via their ForceReregister method.
//
// Declaring the interface here (in argus) avoids an import cycle —
// argus cannot import mcp or settings because those packages import
// argus.
type Reregistrar interface {
	ForceReregister(ctx context.Context) error
}

// RecoverFunc returns a callback suitable for Watcher.OnRestart. The
// returned closure captures every dependency it needs and, when invoked,
// runs the full reconnect sequence:
//
//  1. Transition link state to LinkRecovering (and clear LastError).
//  2. Re-query Daemon.Ports over the unix socket to discover argus's
//     current REST port.
//  3. Atomically update the shared client's baseURL.
//  4. Force a fresh registration POST through both registrars.
//  5. On full success, transition link state back to LinkHealthy.
//     On any sub-step failure, transition to LinkDown and record the
//     wrapped error via SetLinkError so MCP handlers can surface it.
//
// The function is reentrant: callers (the watcher's single-flight gate
// and the heartbeat-404 passive fallback) may invoke it concurrently
// without data races — every underlying operation is independently
// concurrency-safe and the final link-state transition is a single
// atomic store. The cost is that a parallel invocation can briefly
// observe an intermediate LinkRecovering set by the other call; this
// is benign because both invocations are converging on the same target
// state.
func RecoverFunc(ports *PortsClient, client *Client, mcpReg, settingsReg Reregistrar, log *slog.Logger) func(context.Context) {
	if log == nil {
		log = slog.Default()
	}
	return func(ctx context.Context) {
		Recover(ctx, ports, client, mcpReg, settingsReg, log)
	}
}

// Recover runs one pass of the reconnect sequence described on RecoverFunc.
// Most callers should use RecoverFunc to bind the dependencies once and
// hand the returned closure to the watcher and the heartbeat fallback;
// Recover is exported for tests that want to drive the pass synchronously.
func Recover(ctx context.Context, ports *PortsClient, client *Client, mcpReg, settingsReg Reregistrar, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}

	SetLinkState(LinkRecovering)
	SetLinkError(nil)
	log.Info("argus link recovering")

	apiPort, _, err := ports.Ports(ctx)
	if err != nil {
		wrapped := fmt.Errorf("ports query: %w", err)
		SetLinkError(wrapped)
		SetLinkState(LinkDown)
		log.Warn("argus link recovery failed", "stage", "ports", "err", err)
		return
	}

	newURL := fmt.Sprintf("http://127.0.0.1:%d", apiPort)
	client.SetBaseURL(newURL)

	if err := mcpReg.ForceReregister(ctx); err != nil {
		wrapped := fmt.Errorf("mcp re-register: %w", err)
		SetLinkError(wrapped)
		SetLinkState(LinkDown)
		log.Warn("argus link recovery failed", "stage", "mcp", "err", err)
		return
	}

	if err := settingsReg.ForceReregister(ctx); err != nil {
		wrapped := fmt.Errorf("settings re-register: %w", err)
		SetLinkError(wrapped)
		SetLinkState(LinkDown)
		log.Warn("argus link recovery failed", "stage", "settings", "err", err)
		return
	}

	SetLinkError(nil)
	SetLinkState(LinkHealthy)
	log.Info("argus link recovered", "argus_base_url", newURL)
}
