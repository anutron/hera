package view

import (
	"context"
	"encoding/json"

	"github.com/coder/websocket"
)

// controlConn is the subset of pluginview.Conn (and *websocket.Conn) that the
// view-control sender needs: writing a TEXT frame. coder/websocket's
// Conn.Write serializes writers internally, so emitting these TEXT control
// frames concurrently with the SDK's binary surface writes is safe without an
// extra mutex (design.md D12).
type controlConn interface {
	Write(ctx context.Context, typ websocket.MessageType, p []byte) error
}

// HotkeyItem is one advertised binding in the argus key-surrender contract.
// Items flagged Bar:true drive argus's context-sensitive bottom bar; the full
// set drives argus's help overlay. JSON field order is fixed by the struct
// field order so the marshalled shape matches the contract exactly.
type HotkeyItem struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Bar   bool   `json:"bar"`
}

// releaseEnvelope marshals to {"type":"release"} — hand the keyboard back to
// argus (argus blurs, closes the connection, returns to its task list).
type releaseEnvelope struct {
	Type string `json:"type"`
}

// helpEnvelope marshals to {"type":"help"} — pop argus's help overlay rendered
// from hera's pushed hotkey dictionary.
type helpEnvelope struct {
	Type string `json:"type"`
}

// hotkeysEnvelope marshals to {"type":"hotkeys","items":[...]} — populate
// argus's bottom bar (Bar:true items) and help overlay (the full set).
type hotkeysEnvelope struct {
	Type  string       `json:"type"`
	Items []HotkeyItem `json:"items"`
}

// viewControl sends the three argus key-surrender control envelopes as TEXT
// frames over the session's view WebSocket. Bound to the same *websocket.Conn
// hera passes to pluginview.New; coder/websocket serializes writers so these
// writes coexist with the SDK's binary surface writes (design.md D12).
//
// A nil *viewControl is a safe no-op so App / KeyRouter paths built without a
// session conn (tests, daemon startup) never panic.
type viewControl struct {
	ctx  context.Context
	conn controlConn
}

// newViewControl binds a sender to ctx + conn. ctx is the session context so
// writes unblock when the session is torn down.
func newViewControl(ctx context.Context, conn controlConn) *viewControl {
	if ctx == nil {
		ctx = context.Background()
	}
	return &viewControl{ctx: ctx, conn: conn}
}

// SendRelease writes {"type":"release"}.
func (v *viewControl) SendRelease() error {
	return v.send(releaseEnvelope{Type: "release"})
}

// SendHelp writes {"type":"help"}.
func (v *viewControl) SendHelp() error {
	return v.send(helpEnvelope{Type: "help"})
}

// SendHotkeys writes {"type":"hotkeys","items":[...]} with the supplied items.
func (v *viewControl) SendHotkeys(items []HotkeyItem) error {
	return v.send(hotkeysEnvelope{Type: "hotkeys", Items: items})
}

func (v *viewControl) send(envelope any) error {
	if v == nil || v.conn == nil {
		return nil
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return v.conn.Write(v.ctx, websocket.MessageText, data)
}
