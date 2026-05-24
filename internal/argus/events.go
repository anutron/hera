package argus

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Event is one entry on argus's SSE event stream.
type Event struct {
	ID      int64           `json:"id"`
	Type    string          `json:"type"`
	At      time.Time       `json:"at"`
	TaskID  string          `json:"task_id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// EventHandler is invoked once per parsed event.
type EventHandler func(Event)

// StreamEvents subscribes to /api/events/stream as an SSE client and calls
// handler for every parsed event. The call blocks until ctx is canceled or
// the underlying connection fails irrecoverably.
//
// sinceID is sent as ?since=<id>. Pass 0 to receive live events only.
//
// On transient errors (network blip, server restart) StreamEvents
// reconnects with exponential backoff capped at 10 seconds.
func (c *Client) StreamEvents(ctx context.Context, sinceID int64, handler EventHandler) error {
	backoff := time.Second
	const maxBackoff = 10 * time.Second

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := c.streamOnce(ctx, sinceID, handler, func(id int64) { sinceID = id })
		if err == nil {
			return nil // EOF without error; treat as graceful close
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Transient error; wait and retry.
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// streamOnce runs one connect-and-read cycle. Returns nil on graceful EOF,
// an error on transient failures (caller retries).
func (c *Client) streamOnce(ctx context.Context, sinceID int64, handler EventHandler, advance func(int64)) error {
	path := "/api/events/stream"
	if sinceID > 0 {
		path += fmt.Sprintf("?since=%d", sinceID)
	}
	url := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	c.applyAuth(req)
	req.Header.Set("Accept", "text/event-stream")

	// SSE is long-lived; clear the default timeout for this request.
	client := *c.http
	client.Timeout = 0

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("argus.StreamEvents: HTTP %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // up to 1 MiB per event
	var dataBuf strings.Builder
	var eventType string

	for scanner.Scan() {
		line := scanner.Text()

		// Blank line = end of one event block.
		if line == "" {
			if dataBuf.Len() > 0 {
				if ev, ok := parseEvent(dataBuf.String()); ok {
					if eventType != "" && ev.Type == "" {
						ev.Type = eventType
					}
					handler(ev)
					if ev.ID > 0 {
						advance(ev.ID)
					}
				}
			}
			dataBuf.Reset()
			eventType = ""
			continue
		}

		// Comments (keep-alives) start with ":". Ignore.
		if strings.HasPrefix(line, ":") {
			continue
		}

		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			dataBuf.WriteString(payload)
			continue
		}
		// Unknown SSE field; ignore.
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

// parseEvent decodes one data: payload into an Event. Returns false if the
// payload isn't valid JSON (the caller skips).
func parseEvent(data string) (Event, bool) {
	var ev Event
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return Event{}, false
	}
	return ev, true
}
