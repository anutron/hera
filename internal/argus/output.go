package argus

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// TaskOutputSnapshot is the result of GET /api/tasks/{id}/output. Data is the
// tail bytes argus returned (current ring contents or recent on-disk tail);
// Total is the X-Output-Total header value the caller passes back as
// ?since=<n> on the streaming endpoint to attach without overlap or gap.
type TaskOutputSnapshot struct {
	Data  []byte
	Total uint64
}

// GetTaskOutput fetches argus's snapshot of a task's PTY tail. Returns
// (nil, 0, nil) if argus reports 404 with "no output available" — that is the
// normal startup state for a freshly-created task that has not yet emitted
// anything and the proxy treats it as "empty snapshot, attach live at 0".
func (c *Client) GetTaskOutput(ctx context.Context, taskID string) (TaskOutputSnapshot, error) {
	path := "/api/tasks/" + url.PathEscape(taskID) + "/output"
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL()+path, nil)
	if err != nil {
		return TaskOutputSnapshot{}, fmt.Errorf("argus.GetTaskOutput: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return TaskOutputSnapshot{}, fmt.Errorf("argus.GetTaskOutput: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// 404 + "no output available" is the documented empty-snapshot
		// response shape; surface it as an empty snapshot so the proxy can
		// proceed to open the stream at since=0.
		_, _ = io.Copy(io.Discard, resp.Body)
		return TaskOutputSnapshot{}, nil
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return TaskOutputSnapshot{}, &HTTPError{
			Method:     "GET",
			Path:       path,
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}

	var total uint64
	if v := resp.Header.Get("X-Output-Total"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			total = n
		}
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return TaskOutputSnapshot{}, fmt.Errorf("argus.GetTaskOutput: read body: %w", err)
	}
	return TaskOutputSnapshot{Data: data, Total: total}, nil
}

// TaskOutputHandler receives decoded PTY byte chunks delivered by argus's
// per-task SSE stream. The handler MUST NOT retain the slice after returning;
// the caller copies before delivery, but the slice may be reused on the next
// chunk if the implementation chooses.
type TaskOutputHandler func(chunk []byte)

// StreamTaskOutput consumes /api/tasks/{id}/stream?since=N as SSE and invokes
// handler with each decoded byte chunk. Returns nil on graceful EOF
// (received `event: exit`), ctx.Err() when the context is canceled, or an
// error on a transient failure.
//
// Unlike StreamEvents, this method runs exactly one connect-and-read cycle —
// reconnect logic lives at the proxy layer above so it can re-fetch the
// snapshot and resume the cursor without losing bytes.
func (c *Client) StreamTaskOutput(ctx context.Context, taskID string, since uint64, handler TaskOutputHandler) error {
	path := "/api/tasks/" + url.PathEscape(taskID) + "/stream?since=" + strconv.FormatUint(since, 10)
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL()+path, nil)
	if err != nil {
		return fmt.Errorf("argus.StreamTaskOutput: %w", err)
	}
	c.applyAuth(req)
	req.Header.Set("Accept", "text/event-stream")

	// SSE is long-lived; the default 30s timeout would kill it mid-stream.
	client := *c.http
	client.Timeout = 0

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("argus.StreamTaskOutput: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("argus.StreamTaskOutput: HTTP %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var dataBuf strings.Builder
	var eventType string

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()

		if line == "" {
			// End of one SSE event block. argus sends three shapes:
			//
			//   data: <base64>\n\n              (default event: PTY bytes)
			//   event: clipboard\ndata: {...}\n\n
			//   event: exit\ndata: {...}\n\n
			//
			// The proxy cares only about the default (PTY data) event;
			// clipboard and exit are surfaced to the caller via ctx + the
			// EOF return.
			if dataBuf.Len() > 0 {
				payload := dataBuf.String()
				switch eventType {
				case "", "message":
					decoded, err := base64.StdEncoding.DecodeString(payload)
					if err == nil && len(decoded) > 0 {
						handler(decoded)
					}
				case "exit":
					return nil
				}
			}
			dataBuf.Reset()
			eventType = ""
			continue
		}
		if strings.HasPrefix(line, ":") {
			// SSE comment / keep-alive.
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
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}
