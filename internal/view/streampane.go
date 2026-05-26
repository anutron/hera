// Package view renders the hera-view tview application surface: a top bar,
// a three-column body (rail + coord pane + agent pane), and a bottom bar.
//
// streampane.go ports argus's streampane.StreamPane widget: an ANSI byte
// stream consumed from a channel into a bounded in-memory buffer, drawn
// into a bordered tview Box. Newest bytes overwrite oldest once the
// configured cap is reached.
package view

import (
	"regexp"
	"sync"
	"sync/atomic"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// DefaultMaxBytes is the default cap on the internal byte buffer (256 KiB,
// matching argus's per-session ring cap).
const DefaultMaxBytes = 256 * 1024

// ansiRe matches the common ANSI escape sequences (CSI, OSC, simple
// escapes) so they can be stripped before display. Mirrors argus's
// widget.AnsiRe.
var ansiRe = regexp.MustCompile(`\x1b(?:\[[\x20-\x3f]*[\x40-\x7e]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[()][0-9A-B]|[78DEHM])`)

// StreamPaneOption configures a StreamPane at construction time.
type StreamPaneOption func(*StreamPane)

// WithMaxBytes overrides DefaultMaxBytes.
func WithMaxBytes(n int) StreamPaneOption {
	return func(sp *StreamPane) {
		if n > 0 {
			sp.maxBytes = n
		}
	}
}

// StreamPane is a tview Box that renders an ANSI byte stream from a
// channel. Bytes are appended to a bounded internal buffer; the trailing
// lines that fit inside the bordered panel are drawn.
type StreamPane struct {
	*tview.Box

	mu       sync.Mutex
	buf      []byte
	maxBytes int

	touched uint64 // atomic

	// source is the channel currently being drained. Swapped under mu by
	// Replace; the consumer goroutine reads sourceCh / cancelCh under its
	// own snapshot of these references taken inside the loop.
	source <-chan []byte

	// reset, when sent on, signals the consumer goroutine to drop its
	// current source reference and re-read sp.source under the lock.
	reset chan struct{}

	closeOnce sync.Once
	closeCh   chan struct{}
	done      chan struct{}

	// placeholder, when buf is empty, renders centered inside the panel
	// (e.g., "no binding"). Empty string disables the placeholder.
	placeholder string

	// OnNeedRedraw, when set, is invoked once per new byte chunk so the
	// surrounding tview Application can queue a redraw. Safe to leave nil.
	OnNeedRedraw func()
}

// NewStreamPane constructs a StreamPane that drains source into its
// internal buffer. Pass nil for source to start the pane in a detached
// state (placeholder shown until Replace wires a real source). The
// caller may close source to signal end-of-stream; the consumer
// goroutine exits cleanly and the widget keeps displaying whatever
// bytes were already buffered.
func NewStreamPane(source <-chan []byte, opts ...StreamPaneOption) *StreamPane {
	sp := &StreamPane{
		Box:      tview.NewBox(),
		maxBytes: DefaultMaxBytes,
		source:   source,
		reset:    make(chan struct{}, 1),
		closeCh:  make(chan struct{}),
		done:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(sp)
	}
	go sp.consume()
	return sp
}

// Replace swaps the StreamPane's source channel and resets the visible
// buffer to snapshot. Subsequent bytes arrive on ch; passing a nil ch
// detaches the pane (placeholder rendered until the next Replace).
// Safe to call from any goroutine.
func (sp *StreamPane) Replace(snapshot []byte, ch <-chan []byte) {
	sp.mu.Lock()
	sp.buf = append(sp.buf[:0], snapshot...)
	if len(sp.buf) > sp.maxBytes {
		sp.buf = sp.buf[len(sp.buf)-sp.maxBytes:]
	}
	sp.source = ch
	sp.mu.Unlock()
	select {
	case sp.reset <- struct{}{}:
	default:
	}
	if len(snapshot) > 0 {
		atomic.AddUint64(&sp.touched, 1)
		if sp.OnNeedRedraw != nil {
			sp.OnNeedRedraw()
		}
	}
}

// replaceSource is the internal helper used by app.go to atomically
// swap a pane's snapshot + byte channel during rail navigation. It is
// thin wrapper around Replace kept under-package so the public surface
// is just Replace.
func (sp *StreamPane) replaceSource(snapshot []byte, ch <-chan []byte) {
	sp.Replace(snapshot, ch)
}

// SetPlaceholder configures text rendered when the buffer is empty.
// An empty string disables the placeholder.
func (sp *StreamPane) SetPlaceholder(s string) {
	sp.mu.Lock()
	sp.placeholder = s
	sp.mu.Unlock()
}

// Touched returns a monotonic counter that increments every time a new
// non-empty chunk arrives from the source.
func (sp *StreamPane) Touched() uint64 {
	return atomic.LoadUint64(&sp.touched)
}

// Close stops the consumer goroutine. Idempotent.
func (sp *StreamPane) Close() {
	sp.closeOnce.Do(func() { close(sp.closeCh) })
}

// consume drains the current source channel until the StreamPane is
// closed. When Replace swaps the source, it signals via sp.reset; the
// loop re-reads sp.source under the lock and resumes draining the new
// channel. A nil source (detached pane) parks the loop on reset/close
// only.
func (sp *StreamPane) consume() {
	defer close(sp.done)
	for {
		sp.mu.Lock()
		current := sp.source
		sp.mu.Unlock()

		if current == nil {
			select {
			case <-sp.closeCh:
				return
			case <-sp.reset:
				continue
			}
		}

		select {
		case <-sp.closeCh:
			return
		case <-sp.reset:
			continue
		case chunk, ok := <-current:
			if !ok {
				// Source closed without a Replace; park until Replace
				// or Close.
				sp.mu.Lock()
				if sp.source == current {
					sp.source = nil
				}
				sp.mu.Unlock()
				continue
			}
			if len(chunk) == 0 {
				continue
			}
			sp.appendBytes(chunk)
			atomic.AddUint64(&sp.touched, 1)
			if sp.OnNeedRedraw != nil {
				sp.OnNeedRedraw()
			}
		}
	}
}

func (sp *StreamPane) appendBytes(b []byte) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.buf = append(sp.buf, b...)
	if len(sp.buf) > sp.maxBytes {
		// Drop the oldest bytes — keep the trailing maxBytes window.
		excess := len(sp.buf) - sp.maxBytes
		sp.buf = sp.buf[excess:]
	}
}

// Draw paints the pane onto screen. The widget reuses tview.Box's border
// drawing (via DrawForSubclass) and writes its own text into the inner
// rect.
func (sp *StreamPane) Draw(screen tcell.Screen) {
	sp.Box.DrawForSubclass(screen, sp)
	x, y, w, h := sp.GetInnerRect()
	if w <= 0 || h <= 0 {
		return
	}

	sp.mu.Lock()
	buf := append([]byte(nil), sp.buf...)
	placeholder := sp.placeholder
	sp.mu.Unlock()

	if len(buf) == 0 && placeholder != "" {
		row := y + h/2
		col := x + (w-len([]rune(placeholder)))/2
		if col < x {
			col = x
		}
		drawText(screen, col, row, w-(col-x), placeholder, tcell.StyleDefault)
		return
	}

	lines := wrapStripped(buf, w)
	start := 0
	if len(lines) > h {
		start = len(lines) - h
	}
	for i := start; i < len(lines); i++ {
		drawText(screen, x, y+(i-start), w, lines[i], tcell.StyleDefault)
	}
}

// wrapStripped strips ANSI sequences from b and breaks the result into
// display lines no wider than width. Newlines force a line break; runes
// past width wrap to a new line. Returns lines in chronological order.
func wrapStripped(b []byte, width int) []string {
	if width <= 0 {
		return nil
	}
	clean := ansiRe.ReplaceAll(b, nil)

	var (
		lines   []string
		current []rune
	)
	for _, by := range clean {
		switch by {
		case '\n':
			lines = append(lines, string(current))
			current = current[:0]
		case '\r':
			// drop
		default:
			if by < 0x20 {
				continue
			}
			current = append(current, rune(by))
			if len(current) >= width {
				lines = append(lines, string(current))
				current = current[:0]
			}
		}
	}
	if len(current) > 0 {
		lines = append(lines, string(current))
	}
	return lines
}

// drawText writes runes from text into screen at (x, y), clipped to
// maxWidth columns. Mirrors argus's widget.DrawText shape.
func drawText(screen tcell.Screen, x, y, maxWidth int, text string, style tcell.Style) {
	if maxWidth <= 0 {
		return
	}
	col := 0
	for _, r := range text {
		if col >= maxWidth {
			return
		}
		screen.SetContent(x+col, y, r, nil, style)
		col++
	}
}
