package proxy

import (
	"bytes"
	"testing"
)

func TestRing_AppendBelowCapacityRetainsAllBytes(t *testing.T) {
	r := newRing(8)
	r.Append([]byte("hello"))
	data, total := r.Snapshot()
	if string(data) != "hello" {
		t.Fatalf("snapshot = %q, want %q", string(data), "hello")
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
}

func TestRing_AppendsAtCapacityPreserveExactBytes(t *testing.T) {
	r := newRing(8)
	r.Append([]byte("01234567"))
	data, total := r.Snapshot()
	if string(data) != "01234567" {
		t.Fatalf("snapshot = %q", string(data))
	}
	if total != 8 {
		t.Fatalf("total = %d", total)
	}
}

func TestRing_AppendBeyondCapacityDropsOldest(t *testing.T) {
	r := newRing(8)
	r.Append([]byte("0123456789ABCDEF"))
	data, total := r.Snapshot()
	if string(data) != "89ABCDEF" {
		t.Fatalf("snapshot = %q, want %q", string(data), "89ABCDEF")
	}
	if total != 16 {
		t.Fatalf("total = %d", total)
	}
}

func TestRing_AppendsWrapAroundPreserveOrder(t *testing.T) {
	r := newRing(8)
	r.Append([]byte("01234"))
	r.Append([]byte("56789"))
	data, total := r.Snapshot()
	if string(data) != "23456789" {
		t.Fatalf("snapshot = %q", string(data))
	}
	if total != 10 {
		t.Fatalf("total = %d", total)
	}
}

func TestRing_AppendSingleBlobLargerThanCapacityKeepsTail(t *testing.T) {
	r := newRing(4)
	r.Append([]byte("0123456789"))
	data, total := r.Snapshot()
	if string(data) != "6789" {
		t.Fatalf("snapshot = %q, want %q", string(data), "6789")
	}
	if total != 10 {
		t.Fatalf("total = %d", total)
	}
}

func TestRing_SnapshotReturnsIndependentCopy(t *testing.T) {
	r := newRing(8)
	r.Append([]byte("abcde"))
	data, _ := r.Snapshot()
	r.Append([]byte("fgh"))
	if !bytes.Equal(data, []byte("abcde")) {
		t.Fatalf("snapshot mutated after later Append: %q", string(data))
	}
}

func TestRing_DefaultCapacityIs256KiB(t *testing.T) {
	r := newRing(DefaultRingCapacity)
	if cap := r.Capacity(); cap != 256*1024 {
		t.Fatalf("capacity = %d, want %d", cap, 256*1024)
	}
}
