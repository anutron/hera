package argus

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// PluginView is the wire shape returned by /api/plugins/views responses.
// The Scope field is server-derived (from the caller's auth header) and
// is read-only from the client's perspective.
type PluginView struct {
	ID          int64  `json:"id"`
	Scope       string `json:"scope"`
	Title       string `json:"title"`
	Hotkey      string `json:"hotkey,omitempty"`
	CallbackURL string `json:"callback_url"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// pluginViewCreateReq is the POST /api/plugins/views body shape. Scope is
// not included — argus derives it from the auth header so a plugin cannot
// register into another scope's namespace.
type pluginViewCreateReq struct {
	Title       string `json:"title"`
	Hotkey      string `json:"hotkey"`
	CallbackURL string `json:"callback_url"`
}

// pluginViewListResp is the GET /api/plugins/views response shape.
type pluginViewListResp struct {
	Views []PluginView `json:"views"`
}

// ErrPluginViewMissing is returned by HeartbeatView when the registration
// id is no longer present in argus's registry. The caller (a higher-level
// registrar) treats this as a signal to re-register on the next tick.
var ErrPluginViewMissing = errors.New("argus: plugin view registration missing")

// RegisterView registers a top-level plugin view with argus. POST
// /api/plugins/views.
//
// On 201 Created, the newly persisted row is returned. On 409 Conflict
// (the (scope, title) pair is already registered), this transparently
// resolves the existing row via a GET /api/plugins/views lookup and
// returns it. This idempotency is what lets a heartbeat ticker call
// RegisterView every 5 minutes without special-casing "already there" —
// the substrate has no separate refresh endpoint, so re-POST plus
// 409-as-success is the heartbeat shape.
//
// Non-2xx, non-409 responses surface as *HTTPError (use errors.As).
func (c *Client) RegisterView(ctx context.Context, title, hotkey, callbackURL string) (*PluginView, error) {
	body := pluginViewCreateReq{
		Title:       title,
		Hotkey:      hotkey,
		CallbackURL: callbackURL,
	}
	var resp PluginView
	_, err := c.doJSON(ctx, "POST", "/api/plugins/views", body, &resp)
	if err == nil {
		return &resp, nil
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == 409 {
		existing, lookupErr := c.findPluginViewByTitle(ctx, title)
		if lookupErr != nil {
			return nil, fmt.Errorf("argus: register view returned 409 but list lookup failed: %w", lookupErr)
		}
		if existing == nil {
			return nil, fmt.Errorf("argus: register view returned 409 but list did not contain title=%q", title)
		}
		return existing, nil
	}
	return nil, err
}

// HeartbeatView confirms a previously-registered plugin view is still
// present in argus's registry. GET /api/plugins/views (scope-filtered)
// is scanned for id.
//
// Returns nil when the registration is still present, ErrPluginViewMissing
// when it has vanished (caller should re-register), and a typed *HTTPError
// for transport/auth failures (caller should retry on next tick).
func (c *Client) HeartbeatView(ctx context.Context, id int64) error {
	var resp pluginViewListResp
	if _, err := c.doJSON(ctx, "GET", "/api/plugins/views", nil, &resp); err != nil {
		return err
	}
	for _, v := range resp.Views {
		if v.ID == id {
			return nil
		}
	}
	return ErrPluginViewMissing
}

// DeleteView removes a plugin view registration by id. DELETE
// /api/plugins/views/{id}. A 404 is treated as a soft success so callers
// invoking this from shutdown paths don't error when a prior shutdown
// already cleared the registration. Other non-2xx responses surface as
// *HTTPError.
func (c *Client) DeleteView(ctx context.Context, id int64) error {
	_, err := c.doJSON(ctx, "DELETE", "/api/plugins/views/"+strconv.FormatInt(id, 10), nil, nil)
	if err == nil {
		return nil
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == 404 {
		return nil
	}
	return err
}

// findPluginViewByTitle returns the registered view whose Title matches
// within the caller's scope. Argus scope-filters GET /api/plugins/views by
// the auth header, so a Title match is sufficient to identify the row
// uniquely within this plugin's namespace. Returns (nil, nil) on miss.
func (c *Client) findPluginViewByTitle(ctx context.Context, title string) (*PluginView, error) {
	var resp pluginViewListResp
	if _, err := c.doJSON(ctx, "GET", "/api/plugins/views", nil, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Views {
		if resp.Views[i].Title == title {
			return &resp.Views[i], nil
		}
	}
	return nil, nil
}
