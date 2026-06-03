package view

import (
	"bytes"
	"testing"
)

// feedChunks runs a sequence of chunks through one stateful filter and
// concatenates the output — the shape pumpPaneBridge uses (snapshot first,
// then live chunks, same filter instance throughout).
func feedChunks(chunks ...[]byte) []byte {
	var f oscFilter
	var out []byte
	for _, c := range chunks {
		out = append(out, f.filter(c)...)
	}
	return out
}

func TestOSCFilter_InlineBELTitleRemoved(t *testing.T) {
	in := []byte("before\x1b]0;My Title\x07after")
	got := feedChunks(in)
	want := []byte("beforeafter")
	if !bytes.Equal(got, want) {
		t.Fatalf("OSC+BEL not stripped: got %q want %q", got, want)
	}
}

func TestOSCFilter_STTerminatorRemoved(t *testing.T) {
	in := []byte("before\x1b]2;My Title\x1b\\after")
	got := feedChunks(in)
	want := []byte("beforeafter")
	if !bytes.Equal(got, want) {
		t.Fatalf("OSC+ST not stripped: got %q want %q", got, want)
	}
}

func TestOSCFilter_SplitAcrossChunks_Payload(t *testing.T) {
	got := feedChunks(
		[]byte("before\x1b]0;My Ti"),
		[]byte("tle\x07after"),
	)
	want := []byte("beforeafter")
	if !bytes.Equal(got, want) {
		t.Fatalf("payload-split OSC not stripped: got %q want %q", got, want)
	}
}

func TestOSCFilter_SplitAcrossChunks_Introducer(t *testing.T) {
	// Chunk 1 ends on the bare ESC — the filter must defer it until chunk 2
	// reveals the ']' introducer.
	got := feedChunks(
		[]byte("before\x1b"),
		[]byte("]0;My Title\x07after"),
	)
	want := []byte("beforeafter")
	if !bytes.Equal(got, want) {
		t.Fatalf("introducer-split OSC not stripped: got %q want %q", got, want)
	}
}

func TestOSCFilter_SplitAcrossChunks_STTerminator(t *testing.T) {
	// The two-byte ST terminator (ESC \) split across a chunk boundary.
	got := feedChunks(
		[]byte("before\x1b]2;My Title\x1b"),
		[]byte("\\after"),
	)
	want := []byte("beforeafter")
	if !bytes.Equal(got, want) {
		t.Fatalf("ST-split OSC not stripped: got %q want %q", got, want)
	}
}

func TestOSCFilter_ThreeWaySplit(t *testing.T) {
	got := feedChunks(
		[]byte("a\x1b]0;My "),
		[]byte("Long Ti"),
		[]byte("tle\x07b"),
	)
	want := []byte("ab")
	if !bytes.Equal(got, want) {
		t.Fatalf("three-chunk OSC not stripped: got %q want %q", got, want)
	}
}

func TestOSCFilter_0x9CIsNotATerminator(t *testing.T) {
	// Claude's spinner title contains ✳ (E2 9C B3): the 0x9C continuation
	// byte must NOT terminate the OSC (that is the upstream parser bug this
	// filter exists to neutralize) — nothing after it may leak.
	in := []byte("x\x1b]0;\xe2\x9c\xb3 Orchestrate hera-view UX testing\x07y")
	got := feedChunks(in)
	want := []byte("xy")
	if !bytes.Equal(got, want) {
		t.Fatalf("0x9C treated as terminator, payload leaked: got %q want %q", got, want)
	}
}

func TestOSCFilter_CANAndSUBCancel(t *testing.T) {
	for _, cancel := range []byte{0x18, 0x1a} {
		in := []byte{'a', 0x1b, ']', '0', ';', 'T', cancel, 'b'}
		got := feedChunks(in)
		want := []byte("ab")
		if !bytes.Equal(got, want) {
			t.Fatalf("cancel byte %#x: got %q want %q", cancel, got, want)
		}
	}
}

func TestOSCFilter_ESCCancelsOSCAndNewSequenceSurvives(t *testing.T) {
	// An ESC inside an OSC payload that does NOT form ST cancels the OSC;
	// the new sequence (here CSI 31m) must be processed normally and survive.
	in := []byte("a\x1b]0;Title\x1b[31mb")
	got := feedChunks(in)
	want := []byte("a\x1b[31mb")
	if !bytes.Equal(got, want) {
		t.Fatalf("ESC-cancelled OSC mishandled: got %q want %q", got, want)
	}
}

func TestOSCFilter_BackToBackOSC(t *testing.T) {
	// ESC inside an OSC payload immediately opening another OSC (ESC ]).
	in := []byte("a\x1b]0;first\x1b]2;second\x07b")
	got := feedChunks(in)
	want := []byte("ab")
	if !bytes.Equal(got, want) {
		t.Fatalf("back-to-back OSC mishandled: got %q want %q", got, want)
	}
}

func TestOSCFilter_NonOSCEscapesPassThrough(t *testing.T) {
	// SGR colors and cursor-positioning CSI must survive byte-for-byte.
	in := []byte("\x1b[31mred\x1b[0m \x1b[6Gcol6 \x1b(Bcharset")
	got := feedChunks(in)
	if !bytes.Equal(got, in) {
		t.Fatalf("non-OSC escapes mangled: got %q want %q", got, in)
	}
}

func TestOSCFilter_MixedStream(t *testing.T) {
	in := []byte("\x1b[1mbold\x1b]0;Title One\x07 mid \x1b[32mgreen\x1b]2;Title Two\x1b\\ end\x1b[0m")
	got := feedChunks(in)
	want := []byte("\x1b[1mbold mid \x1b[32mgreen end\x1b[0m")
	if !bytes.Equal(got, want) {
		t.Fatalf("mixed stream: got %q want %q", got, want)
	}
}

func TestOSCFilter_RunawayCapStopsDropping(t *testing.T) {
	// An OSC that never terminates must stop being dropped after the cap so
	// the filter can never swallow a stream unboundedly.
	in := append([]byte("a\x1b]0;"), bytes.Repeat([]byte("x"), maxOSCDropBytes+10)...)
	got := feedChunks(in)
	if len(got) <= 1 {
		t.Fatalf("runaway OSC swallowed the stream: only %d bytes survived", len(got))
	}
	if got[0] != 'a' {
		t.Fatalf("leading text lost: got %q...", got[:1])
	}
}

func TestOSCFilter_LoneTrailingESCDeferred(t *testing.T) {
	// A chunk ending in a bare ESC must not emit it yet — the next chunk
	// decides whether it opens an OSC. Here it does not (CSI), so the ESC
	// and the CSI body must both come through once chunk 2 arrives.
	var f oscFilter
	out1 := f.filter([]byte("abc\x1b"))
	if !bytes.Equal(out1, []byte("abc")) {
		t.Fatalf("chunk 1: got %q want %q (ESC must be held back)", out1, "abc")
	}
	out2 := f.filter([]byte("[31mdef"))
	if !bytes.Equal(out2, []byte("\x1b[31mdef")) {
		t.Fatalf("chunk 2: got %q want %q (deferred ESC must re-emit)", out2, "\x1b[31mdef")
	}
}

func TestOSCFilter_FreshSliceOwnershipPerCall(t *testing.T) {
	// pumpPaneBridge sends filtered chunks onto a channel consumed by another
	// goroutine, so each call's output must own its backing array — a later
	// call must not overwrite an earlier call's bytes.
	var f oscFilter
	first := f.filter([]byte("first"))
	_ = f.filter([]byte("SECOND"))
	if !bytes.Equal(first, []byte("first")) {
		t.Fatalf("earlier output clobbered by later filter call: got %q", first)
	}
}
