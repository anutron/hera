package mcp

import (
	"errors"
	"strings"
	"testing"

	"github.com/anutron/hera/internal/argus"
)

// resetLink restores argus link state to healthy at the end of each test.
// Package-level state would otherwise leak across tests in this file.
func resetLink(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		argus.SetLinkError(nil)
		argus.SetLinkState(argus.LinkHealthy)
	})
}

func TestLinkGate_HealthyProceeds(t *testing.T) {
	resetLink(t)
	argus.SetLinkState(argus.LinkHealthy)

	resp, gated := LinkGate()
	if gated {
		t.Fatalf("healthy state should not gate; got resp=%+v", resp)
	}
}

func TestLinkGate_RecoveringReturnsStructuredError(t *testing.T) {
	resetLink(t)
	argus.SetLinkState(argus.LinkRecovering)

	resp, gated := LinkGate()
	if !gated {
		t.Fatalf("recovering state must gate the handler")
	}
	if !resp.IsError {
		t.Fatalf("gated response must have IsError=true")
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" {
		t.Fatalf("gated response must carry exactly one text block, got %+v", resp.Content)
	}
	if resp.Content[0].Text != "argus link recovering, retry in a moment" {
		t.Fatalf("recovering text = %q", resp.Content[0].Text)
	}
}

func TestLinkGate_DownReturnsStructuredErrorWithLastError(t *testing.T) {
	resetLink(t)
	argus.SetLinkError(errors.New("dial unix /Users/aaron/.argus/daemon.sock: connect: no such file or directory"))
	argus.SetLinkState(argus.LinkDown)

	resp, gated := LinkGate()
	if !gated {
		t.Fatalf("down state must gate the handler")
	}
	if !resp.IsError {
		t.Fatalf("gated response must have IsError=true")
	}
	want := "argus link down: dial unix /Users/aaron/.argus/daemon.sock: connect: no such file or directory"
	if resp.Content[0].Text != want {
		t.Fatalf("down text = %q, want %q", resp.Content[0].Text, want)
	}
}

func TestLinkGate_DownWithoutLastErrorOmitsSuffix(t *testing.T) {
	resetLink(t)
	argus.SetLinkError(nil)
	argus.SetLinkState(argus.LinkDown)

	resp, gated := LinkGate()
	if !gated {
		t.Fatalf("down state must gate the handler")
	}
	// Spec wording: `argus link down: <LastError>`. When LastError is nil
	// (theoretically — recovery should always record one when transitioning
	// to down), fall back to a bare "argus link down" without the trailing
	// colon-blank so the message stays human-readable.
	got := resp.Content[0].Text
	if !strings.HasPrefix(got, "argus link down") {
		t.Fatalf("down text should start with 'argus link down', got %q", got)
	}
	if strings.HasSuffix(got, ": ") {
		t.Fatalf("down text should not have trailing ': ' when LastError is nil, got %q", got)
	}
}

// TestLinkGate_WiredIntoHandlers proves the preamble actually fires from a
// real tool handler. SendHandler is the canary; if it short-circuits when
// the link is recovering or down without touching its dependencies, the
// other hera_* handlers using the same preamble are equivalently gated.
func TestLinkGate_WiredIntoSendHandler(t *testing.T) {
	resetLink(t)
	argus.SetLinkState(argus.LinkRecovering)

	// Pass nil deps: if the gate fires, the handler never touches them.
	h := NewSendHandler(nil, nil, nil, true, 300000)
	resp := h.Handle(t.Context(), []byte(`{"cwd":"/tmp/anywhere","body":"hi"}`))
	if !resp.IsError {
		t.Fatalf("expected gated error during recovering, got success: %+v", resp)
	}
	if resp.Content[0].Text != "argus link recovering, retry in a moment" {
		t.Fatalf("send did not return preamble text; got %q", resp.Content[0].Text)
	}
}

func TestLinkGate_WiredIntoInboxHandler(t *testing.T) {
	resetLink(t)
	argus.SetLinkError(errors.New("ports rpc failed"))
	argus.SetLinkState(argus.LinkDown)

	h := NewInboxHandler(nil, nil, nil)
	resp := h.Handle(t.Context(), []byte(`{"cwd":"/tmp/anywhere"}`))
	if !resp.IsError {
		t.Fatalf("expected gated error during down, got success: %+v", resp)
	}
	if resp.Content[0].Text != "argus link down: ports rpc failed" {
		t.Fatalf("inbox did not return preamble text; got %q", resp.Content[0].Text)
	}
}
