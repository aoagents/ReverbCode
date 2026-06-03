package terminal

import (
	"bytes"
	"strings"
	"testing"
)

func TestRingBufferKeepsTailWithinCap(t *testing.T) {
	r := newRingBuffer(8)
	r.append([]byte("abcd"))
	r.append([]byte("efgh"))
	r.append([]byte("ij")) // total 10 > 8, drop oldest 2

	if got := string(r.snapshot()); got != "cdefghij" {
		t.Fatalf("snapshot = %q, want %q", got, "cdefghij")
	}
}

func TestRingBufferTruncatesOversizeWrite(t *testing.T) {
	r := newRingBuffer(4)
	r.append([]byte(strings.Repeat("x", 3)))
	r.append([]byte("abcdefgh")) // single write larger than cap

	if got := string(r.snapshot()); got != "efgh" {
		t.Fatalf("snapshot = %q, want %q", got, "efgh")
	}
}

func TestRingBufferSnapshotIsCopy(t *testing.T) {
	r := newRingBuffer(16)
	r.append([]byte("data"))
	snap := r.snapshot()
	snap[0] = 'X'
	if !bytes.Equal(r.snapshot(), []byte("data")) {
		t.Fatal("snapshot must not alias internal buffer")
	}
}
