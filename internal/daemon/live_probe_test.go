package daemon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// renderANSIToText turns a full-surface ANSI byte stream (what the wsscreen
// emits) into rough text: it emits a newline whenever the cursor is
// positioned (CSI ... H/f), drops other escape sequences (colors, etc.), and
// keeps printable + multibyte (nerd-font glyph) bytes. Good enough to read the
// rail rows, icons, counts, and pane content without a full vt emulator.
func renderANSIToText(b []byte) string {
	var out bytes.Buffer
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c == 0x1b { // ESC
			if i+1 < len(b) && b[i+1] == '[' { // CSI
				j := i + 2
				for j < len(b) && (b[j] < '@' || b[j] > '~') {
					j++
				}
				if j < len(b) {
					final := b[j]
					if final == 'H' || final == 'f' {
						out.WriteByte('\n')
					}
					i = j
					continue
				}
			}
			// other escape: skip ESC + next byte
			i++
			continue
		}
		if c == '\n' || c == '\t' || c >= 0x20 {
			out.WriteByte(c)
		}
	}
	// collapse runs of blank lines
	lines := strings.Split(out.String(), "\n")
	var keep []string
	for _, ln := range lines {
		ln = strings.TrimRight(ln, " ")
		if strings.TrimSpace(ln) == "" {
			continue
		}
		keep = append(keep, ln)
	}
	return strings.Join(keep, "\n")
}

// renderANSIToGrid reconstructs a 2D character grid from a full-surface ANSI
// byte stream by honoring cursor-position (CSI row;col H/f), CR, LF, and
// clear-screen (CSI 2J) — so cursor-addressed full repaints AND incremental
// diffs land in the right cells. Unlike renderANSIToText (which fragments
// when live panes stream many positioned writes), this yields a stable
// snapshot of the final screen. UTF-8 multibyte glyphs (nerd-font icons)
// occupy one cell. Returns the grid as newline-joined rows, right-trimmed.
func renderANSIToGrid(b []byte, rows, cols int) string {
	grid := make([][]rune, rows)
	for i := range grid {
		grid[i] = make([]rune, cols)
		for j := range grid[i] {
			grid[i][j] = ' '
		}
	}
	cr, cc := 0, 0
	rs := []rune(string(b))
	atoi := func(s string, def int) int {
		if s == "" {
			return def
		}
		n := 0
		for _, c := range s {
			if c < '0' || c > '9' {
				return def
			}
			n = n*10 + int(c-'0')
		}
		return n
	}
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == 0x1b:
			if i+1 < len(rs) && rs[i+1] == '[' {
				j := i + 2
				for j < len(rs) && (rs[j] < '@' || rs[j] > '~') {
					j++
				}
				if j < len(rs) {
					params := string(rs[i+2 : j])
					switch rs[j] {
					case 'H', 'f':
						r, cmid := 1, 1
						if k := strings.IndexByte(params, ';'); k >= 0 {
							r = atoi(params[:k], 1)
							cmid = atoi(params[k+1:], 1)
						} else {
							r = atoi(params, 1)
						}
						cr, cc = r-1, cmid-1
					case 'J':
						for a := range grid {
							for bb := range grid[a] {
								grid[a][bb] = ' '
							}
						}
					}
					i = j
					continue
				}
			}
			i++ // skip ESC + next on non-CSI
		case c == '\n':
			cr++
			cc = 0
		case c == '\r':
			cc = 0
		case c >= 0x20:
			if cr >= 0 && cr < rows && cc >= 0 && cc < cols {
				grid[cr][cc] = c
			}
			cc++
		}
	}
	var out []string
	for _, row := range grid {
		out = append(out, strings.TrimRight(string(row), " "))
	}
	// collapse trailing blank rows
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// TestLiveViewProbe connects to the live hera /view, renders the surface, and
// dumps it as text so the agent can SEE the rail (rows, icons, counts, panes)
// and iterate without screenshots. Gated on HERA_LIVE_PROBE=1. The full
// workflow — reading the grid, driving keys, spawning fixtures, redeploying —
// is documented in the `hera-view-probe` skill (.claude/skills/hera-view-probe).
//
//	# see the rail
//	HERA_LIVE_PROBE=1 go test ./internal/daemon/ -run LiveViewProbe -count=1 -v -timeout 30s
//	# drive navigation: HERA_PROBE_KEYS sends each char as a key (j/k move the
//	# rail cursor), then the settled surface is captured
//	HERA_LIVE_PROBE=1 HERA_PROBE_KEYS=jjjjjjjjjj go test ./internal/daemon/ -run LiveViewProbe -count=1 -v -timeout 50s
//
// The surface is reconstructed by renderANSIToGrid (cursor-addressed), which
// survives live pane streaming that fragments a linear ANSI dump.
func TestLiveViewProbe(t *testing.T) {
	if os.Getenv("HERA_LIVE_PROBE") != "1" {
		t.Skip("set HERA_LIVE_PROBE=1 to probe the live view")
	}
	addr := os.Getenv("HERA_VIEW_ADDR")
	if addr == "" {
		addr = "127.0.0.1:7744"
	}
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, "ws://"+addr+"/view", nil)
	if err != nil {
		t.Fatalf("dial /view: %v", err)
	}
	defer conn.CloseNow()

	readCtx, readCancel := context.WithCancel(context.Background())
	defer readCancel()
	// Accumulate all surface bytes into a mutex-guarded buffer so the reader
	// never blocks (a channel overflows during multi-key navigation, which
	// stalls the WebSocket read and loses the final frame). We snapshot the
	// last full repaint after the keystrokes settle.
	var (
		mu  sync.Mutex
		buf bytes.Buffer
		ctl []string // hera → client TEXT control frames (hotkeys / release / help)
	)
	go func() {
		for {
			typ, data, err := conn.Read(readCtx)
			if err != nil {
				return
			}
			mu.Lock()
			if typ == websocket.MessageBinary {
				buf.Write(data)
			} else if typ == websocket.MessageText {
				ctl = append(ctl, string(data))
			}
			mu.Unlock()
		}
	}()

	// Size the surface generously so the rail + both panes render.
	// HERA_PROBE_SIZE=COLSxROWS overrides — e.g. a short surface (200x18)
	// makes the rail overflow, so wheel-pan behavior is observable live.
	cols, rows := 200, 55
	if size := os.Getenv("HERA_PROBE_SIZE"); size != "" {
		if n, err := fmt.Sscanf(size, "%dx%d", &cols, &rows); n != 2 || err != nil {
			t.Fatalf("HERA_PROBE_SIZE %q: want COLSxROWS", size)
		}
	}
	_ = conn.Write(context.Background(), websocket.MessageText,
		[]byte(fmt.Sprintf(`{"type":"resize","cols":%d,"rows":%d}`, cols, rows)))

	time.Sleep(500 * time.Millisecond) // let the first frame render

	// HERA_PROBE_KEYS: each byte sent as its own binary frame (j/k nav, a, s, n …).
	if keys := os.Getenv("HERA_PROBE_KEYS"); keys != "" {
		for _, k := range []byte(keys) {
			_ = conn.Write(context.Background(), websocket.MessageBinary, []byte{k})
			time.Sleep(220 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond)
	}

	// HERA_PROBE_RAW: a `;;`-separated SEQUENCE of Go-quoted byte strings, each
	// sent as ONE binary frame (so a multi-byte escape sequence like Ctrl-Right
	// "\x1b[1;5C" is parsed as one key). Lets a test express a full functional
	// step like "Enter into the pane, then type x": HERA_PROBE_RAW='\r;;x'.
	if raw := os.Getenv("HERA_PROBE_RAW"); raw != "" {
		for _, tok := range strings.Split(raw, ";;") {
			if decoded, err := strconv.Unquote(`"` + tok + `"`); err == nil {
				_ = conn.Write(context.Background(), websocket.MessageBinary, []byte(decoded))
				time.Sleep(350 * time.Millisecond)
			} else {
				t.Logf("HERA_PROBE_RAW token %q unquote failed: %v", tok, err)
			}
		}
	}

	// Force one clean full repaint of the settled surface (tcell clears + redraws
	// on EventResize); renderANSIToGrid honors the clear so the snapshot is final.
	_ = conn.Write(context.Background(), websocket.MessageText,
		[]byte(fmt.Sprintf(`{"type":"resize","cols":%d,"rows":%d}`, cols-2, rows-2)))
	time.Sleep(1300 * time.Millisecond)

	mu.Lock()
	full := buf.Bytes()
	frames := append([]string(nil), ctl...)
	mu.Unlock()
	if len(full) == 0 {
		t.Fatal("no surface bytes — daemon not rendering")
	}
	// Reconstruct the final screen via a cursor-addressed grid so live pane
	// streaming doesn't fragment the snapshot.
	t.Logf("=== LIVE HERA SURFACE (%d bytes) ===\n%s", len(full), renderANSIToGrid(full, 56, 205))
	// hera's outbound control frames — the key-surrender contract chrome
	// (hotkeys → argus bottom bar, release on Esc-from-RAIL, help on ?).
	// The direct probe bypasses argus, so the bottom bar itself is NOT in the
	// surface above; these frames are how we verify that behavior functionally.
	t.Logf("=== HERA CONTROL FRAMES (%d) ===\n%s", len(frames), strings.Join(frames, "\n"))
}

// TestLiveArgusAPIProbe GETs the live argus daemon's /api/tasks and reports
// which task-state fields it serves. Gated on HERA_LIVE_PROBE=1.
func TestLiveArgusAPIProbe(t *testing.T) {
	if os.Getenv("HERA_LIVE_PROBE") != "1" {
		t.Skip("set HERA_LIVE_PROBE=1 to probe the live argus daemon")
	}
	base := os.Getenv("ARGUS_BASE_URL")
	if base == "" {
		base = "http://127.0.0.1:7743"
	}
	tok := os.Getenv("ARGUS_TOKEN")
	if tok == "" {
		if home, err := os.UserHomeDir(); err == nil {
			if b, err := os.ReadFile(home + "/.hera/api-token"); err == nil {
				tok = strings.TrimSpace(string(b))
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/tasks?archived=all", nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s/api/tasks: %v", base, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 400_000))
	s := string(body)
	t.Logf("HTTP %d, %d bytes", resp.StatusCode, len(body))
	report := func(field string) string {
		if strings.Contains(s, `"`+field+`"`) {
			return "PRESENT"
		}
		return "absent"
	}
	t.Logf("served fields: status=%s idle=%s needs_input=%s archived=%s",
		report("status"), report("idle"), report("needs_input"), report("archived"))
}
