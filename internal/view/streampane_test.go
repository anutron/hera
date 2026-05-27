package view

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestStreamPane_TouchedIncrementsOnNewBytes(t *testing.T) {
	src := make(chan []byte, 1)
	sp := NewStreamPane(src)
	defer sp.Close()

	before := sp.Touched()
	src <- []byte("hello")
	waitForTouched(t, sp, before+1)
}

func TestStreamPane_TouchedDoesNotIncrementOnEmptyChunks(t *testing.T) {
	src := make(chan []byte, 2)
	sp := NewStreamPane(src)
	defer sp.Close()

	src <- []byte("hi")
	waitForTouched(t, sp, 1)
	got := sp.Touched()
	src <- []byte("")
	// Give the goroutine a moment; empty chunk should be a no-op.
	time.Sleep(20 * time.Millisecond)
	if sp.Touched() != got {
		t.Fatalf("Touched advanced on empty chunk: was %d, now %d", got, sp.Touched())
	}
}

func TestStreamPane_DrawShowsBytes(t *testing.T) {
	src := make(chan []byte, 1)
	sp := NewStreamPane(src)
	defer sp.Close()
	sp.SetBorder(true)
	sp.SetTitle("Logs")

	src <- []byte("hello world\n")
	waitForTouched(t, sp, 1)

	sim := newSimScreen(t, 30, 6)
	sp.SetRect(0, 0, 30, 6)
	sp.Draw(sim)
	sim.Show()

	row := readRow(sim, 1, 30)
	if !strings.Contains(row, "hello world") {
		t.Fatalf("expected 'hello world' on row 1; got %q", row)
	}
}

func TestStreamPane_DrawRendersTitleInBorder(t *testing.T) {
	src := make(chan []byte)
	sp := NewStreamPane(src)
	defer sp.Close()
	sp.SetBorder(true)
	sp.SetTitle("MyTitle")

	sim := newSimScreen(t, 20, 4)
	sp.SetRect(0, 0, 20, 4)
	sp.Draw(sim)
	sim.Show()

	top := readRow(sim, 0, 20)
	if !strings.Contains(top, "MyTitle") {
		t.Fatalf("expected title 'MyTitle' on top border; got %q", top)
	}
}

func TestStreamPane_DrawStripsAnsi(t *testing.T) {
	src := make(chan []byte, 1)
	sp := NewStreamPane(src)
	defer sp.Close()
	sp.SetBorder(true)

	src <- []byte("\x1b[31mred\x1b[0m\n")
	waitForTouched(t, sp, 1)

	sim := newSimScreen(t, 20, 4)
	sp.SetRect(0, 0, 20, 4)
	sp.Draw(sim)
	sim.Show()

	row := readRow(sim, 1, 20)
	if !strings.Contains(row, "red") {
		t.Fatalf("expected 'red' on row 1; got %q", row)
	}
	if strings.Contains(row, "\x1b") {
		t.Errorf("escape sequence leaked into output: %q", row)
	}
}

func TestStreamPane_BoundedBufferDropsOldest(t *testing.T) {
	src := make(chan []byte, 4)
	sp := NewStreamPane(src, WithMaxBytes(16))
	defer sp.Close()

	src <- []byte("aaaaaaaa\n")
	src <- []byte("bbbbbbbb\n")
	src <- []byte("cccccccc\n")
	waitForTouched(t, sp, 3)

	sp.mu.Lock()
	got := len(sp.buf)
	sp.mu.Unlock()
	if got > 16 {
		t.Fatalf("buffer length %d exceeds cap 16", got)
	}
}

func TestStreamPane_CloseIsIdempotent(t *testing.T) {
	src := make(chan []byte)
	sp := NewStreamPane(src)
	sp.Close()
	sp.Close() // must not panic
}

func TestStreamPane_CloseStopsConsumer(t *testing.T) {
	src := make(chan []byte)
	sp := NewStreamPane(src)
	sp.Close()
	select {
	case <-sp.done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("consumer goroutine did not exit after Close")
	}
}

func TestStreamPane_SourceCloseParksConsumerThenCloseExits(t *testing.T) {
	// Source close detaches the consumer (the pane keeps its buffered
	// content waiting for the next Replace); Close still ends the
	// goroutine cleanly.
	src := make(chan []byte)
	sp := NewStreamPane(src)
	close(src)
	// Give the goroutine a moment to observe the closed channel.
	time.Sleep(20 * time.Millisecond)
	select {
	case <-sp.done:
		t.Fatal("consumer exited prematurely on source close; expected parked-until-Close")
	default:
	}
	sp.Close()
	select {
	case <-sp.done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("consumer did not exit after Close")
	}
}

func TestStreamPane_ReplaceSwapsSourceAndResetsBuffer(t *testing.T) {
	src1 := make(chan []byte, 1)
	sp := NewStreamPane(src1)
	defer sp.Close()
	sp.SetBorder(true)

	src1 <- []byte("alpha")
	waitForTouched(t, sp, 1)

	src2 := make(chan []byte, 1)
	sp.Replace([]byte("beta-snap\n"), src2)

	sim := newSimScreen(t, 30, 6)
	sp.SetRect(0, 0, 30, 6)
	sp.Draw(sim)
	sim.Show()
	full := readScreen(sim)
	if !strings.Contains(full, "beta-snap") {
		t.Fatalf("expected snapshot 'beta-snap' rendered after Replace; got:\n%s", full)
	}
	if strings.Contains(full, "alpha") {
		t.Fatalf("expected previous source content 'alpha' to be gone after Replace; got:\n%s", full)
	}

	// New source bytes should now be drained.
	before := sp.Touched()
	src2 <- []byte("gamma\n")
	waitForTouched(t, sp, before+1)
}

func TestStreamPane_ReplaceWithNilDetachesPane(t *testing.T) {
	src := make(chan []byte, 1)
	sp := NewStreamPane(src)
	defer sp.Close()
	sp.SetBorder(true)

	src <- []byte("foo")
	waitForTouched(t, sp, 1)

	sp.Replace(nil, nil)
	sp.SetPlaceholder("(detached)")

	sim := newSimScreen(t, 30, 6)
	sp.SetRect(0, 0, 30, 6)
	sp.Draw(sim)
	sim.Show()
	full := readScreen(sim)
	if !strings.Contains(full, "(detached)") {
		t.Fatalf("expected placeholder visible after Replace(nil, nil); got:\n%s", full)
	}
}

func TestStreamPane_OnRedrawFiresAfterBytes(t *testing.T) {
	src := make(chan []byte, 1)
	sp := NewStreamPane(src)
	defer sp.Close()

	var (
		mu    sync.Mutex
		count int
	)
	sp.OnNeedRedraw = func() {
		mu.Lock()
		count++
		mu.Unlock()
	}

	src <- []byte("x")
	waitForTouched(t, sp, 1)

	mu.Lock()
	defer mu.Unlock()
	if count < 1 {
		t.Fatalf("expected OnNeedRedraw to fire at least once, got %d", count)
	}
}

func TestStreamPane_DrawIgnoresControlCharsAndCR(t *testing.T) {
	src := make(chan []byte, 1)
	sp := NewStreamPane(src)
	defer sp.Close()
	sp.SetBorder(true)

	src <- []byte("hi\x01\x02\rthere\n")
	waitForTouched(t, sp, 1)

	sim := newSimScreen(t, 20, 4)
	sp.SetRect(0, 0, 20, 4)
	sp.Draw(sim)
	sim.Show()
	row := readRow(sim, 1, 20)
	if !strings.Contains(row, "hithere") {
		t.Fatalf("expected 'hithere' after control-char strip; got %q", row)
	}
}

func TestStreamPane_DrawShowsPlaceholderWhenEmpty(t *testing.T) {
	src := make(chan []byte)
	sp := NewStreamPane(src)
	defer sp.Close()
	sp.SetBorder(true)
	sp.SetPlaceholder("no binding")

	sim := newSimScreen(t, 30, 6)
	sp.SetRect(0, 0, 30, 6)
	sp.Draw(sim)
	sim.Show()

	full := readScreen(sim)
	if !strings.Contains(full, "no binding") {
		t.Fatalf("expected placeholder 'no binding' visible; got:\n%s", full)
	}
}

func TestStreamPane_DrawScrollsLastLines(t *testing.T) {
	src := make(chan []byte, 1)
	sp := NewStreamPane(src)
	defer sp.Close()
	sp.SetBorder(true)

	src <- []byte("one\ntwo\nthree\nfour\nfive\n")
	waitForTouched(t, sp, 1)

	sim := newSimScreen(t, 20, 4)
	sp.SetRect(0, 0, 20, 4)
	sp.Draw(sim)
	sim.Show()

	full := readScreen(sim)
	if !strings.Contains(full, "five") {
		t.Fatalf("expected trailing line 'five' in viewport; got:\n%s", full)
	}
}

func TestStreamPane_DrawHandlesZeroRect(t *testing.T) {
	src := make(chan []byte)
	sp := NewStreamPane(src)
	defer sp.Close()
	sim := newSimScreen(t, 10, 4)
	sp.SetRect(0, 0, 0, 0)
	sp.Draw(sim) // must not panic
}

func TestWrapStripped_PreservesUTF8MultiByteGlyphs(t *testing.T) {
	// Box-drawing characters and arrows are multi-byte UTF-8. The previous
	// byte-iterating loop in wrapStripped exploded each into individual
	// bytes, producing mojibake like `â`/`Â`. Assert the exact runes survive.
	in := []byte("─→│┌┘")
	lines := wrapStripped(in, 20)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %v", len(lines), lines)
	}
	want := "─→│┌┘"
	if lines[0] != want {
		t.Fatalf("rendered line mojibake'd: got %q want %q", lines[0], want)
	}
	runes := []rune(lines[0])
	if len(runes) != 5 {
		t.Fatalf("expected 5 runes, got %d (%v)", len(runes), runes)
	}
}

func TestStreamPane_DrawShowsUTF8Glyphs(t *testing.T) {
	src := make(chan []byte, 1)
	sp := NewStreamPane(src)
	defer sp.Close()

	src <- []byte("┌─┘\n")
	waitForTouched(t, sp, 1)

	sim := newSimScreen(t, 20, 4)
	sp.SetRect(0, 0, 20, 4)
	sp.Draw(sim)
	sim.Show()

	row := readRow(sim, 0, 20)
	if !strings.Contains(row, "┌─┘") {
		t.Errorf("expected box-drawing glyphs in row, got %q", row)
	}
}

func TestWrapStripped_ZeroWidthIsNoOp(t *testing.T) {
	got := wrapStripped([]byte("abc"), 0)
	if len(got) != 0 {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestWithMaxBytes_ZeroIsIgnored(t *testing.T) {
	src := make(chan []byte)
	sp := NewStreamPane(src, WithMaxBytes(0))
	defer sp.Close()
	if sp.maxBytes != DefaultMaxBytes {
		t.Fatalf("expected DefaultMaxBytes when zero; got %d", sp.maxBytes)
	}
}

// --- helpers ---

func waitForTouched(t *testing.T, sp *StreamPane, want uint64) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if sp.Touched() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Touched did not reach %d (got %d)", want, sp.Touched())
}

func newSimScreen(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(w, h)
	return s
}

func readRow(s tcell.SimulationScreen, row, w int) string {
	cells, cw, _ := s.GetContents()
	if row < 0 || row*cw >= len(cells) {
		return ""
	}
	var b strings.Builder
	for col := 0; col < w; col++ {
		idx := row*cw + col
		if idx >= len(cells) {
			break
		}
		for _, r := range cells[idx].Runes {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func readScreen(s tcell.SimulationScreen) string {
	cells, cw, h := s.GetContents()
	var b strings.Builder
	for row := 0; row < h; row++ {
		for col := 0; col < cw; col++ {
			idx := row*cw + col
			if idx >= len(cells) {
				break
			}
			for _, r := range cells[idx].Runes {
				if r == 0 {
					b.WriteRune(' ')
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteRune('\n')
	}
	return b.String()
}
