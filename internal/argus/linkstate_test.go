package argus

import (
	"errors"
	"sync"
	"testing"
)

func TestLinkState_String(t *testing.T) {
	cases := map[LinkState]string{
		LinkHealthy:    "healthy",
		LinkRecovering: "recovering",
		LinkDown:       "down",
		LinkState(99):  "unknown",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("LinkState(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestLinkState_DefaultIsHealthy(t *testing.T) {
	// Reset to verify the zero-value semantics rather than whatever prior
	// tests left behind.
	resetLinkForTest(t)
	if got := GetLinkState(); got != LinkHealthy {
		t.Fatalf("default state = %v, want LinkHealthy", got)
	}
}

func TestLinkState_SetGet(t *testing.T) {
	resetLinkForTest(t)
	SetLinkState(LinkRecovering)
	if got := GetLinkState(); got != LinkRecovering {
		t.Fatalf("after SetLinkState(LinkRecovering) = %v", got)
	}
	SetLinkState(LinkDown)
	if got := GetLinkState(); got != LinkDown {
		t.Fatalf("after SetLinkState(LinkDown) = %v", got)
	}
	SetLinkState(LinkHealthy)
	if got := GetLinkState(); got != LinkHealthy {
		t.Fatalf("after SetLinkState(LinkHealthy) = %v", got)
	}
}

func TestLinkState_LastErrorRoundTrip(t *testing.T) {
	resetLinkForTest(t)
	if err := LinkLastError(); err != nil {
		t.Fatalf("LinkLastError() = %v, want nil", err)
	}
	want := errors.New("ports rpc: dial unix /Users/aaron/.argus/daemon.sock: no such file")
	SetLinkError(want)
	if got := LinkLastError(); got == nil || got.Error() != want.Error() {
		t.Fatalf("LinkLastError() = %v, want %v", got, want)
	}
	SetLinkError(nil)
	if err := LinkLastError(); err != nil {
		t.Fatalf("LinkLastError() after clear = %v, want nil", err)
	}
}

// TestLinkState_ConcurrentSetGet exercises the atomic state path under
// the race detector. Run with `go test -race`.
func TestLinkState_ConcurrentSetGet(t *testing.T) {
	resetLinkForTest(t)
	var wg sync.WaitGroup
	const goroutines = 16
	for i := 0; i < goroutines; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				SetLinkState(LinkState(i % 3))
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_ = GetLinkState()
			}
		}()
	}
	wg.Wait()
}

// resetLinkForTest restores package-level link state to healthy so tests
// don't leak into each other.
func resetLinkForTest(t *testing.T) {
	t.Helper()
	SetLinkError(nil)
	SetLinkState(LinkHealthy)
	t.Cleanup(func() {
		SetLinkError(nil)
		SetLinkState(LinkHealthy)
	})
}
