package terminal

// defaultRingMax caps per-terminal replay history. A late subscriber gets at
// most this many bytes of recent output so it can paint a usable screen without
// the whole session backlog. Matches the legacy 50KB ring.
const defaultRingMax = 50 * 1024

// ringBuffer is a byte ring holding the most recent output of one terminal. It
// is owned by session and accessed under session.mu.
type ringBuffer struct {
	buf []byte
	max int
}

func newRingBuffer(maxBytes int) *ringBuffer {
	if maxBytes <= 0 {
		maxBytes = defaultRingMax
	}
	return &ringBuffer{max: maxBytes}
}

// append adds p and drops the oldest bytes beyond max. A single write larger
// than max is truncated to its last max bytes.
func (r *ringBuffer) append(p []byte) {
	if len(p) >= r.max {
		r.buf = append(r.buf[:0], p[len(p)-r.max:]...)
		return
	}
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.max {
		r.buf = append(r.buf[:0], r.buf[len(r.buf)-r.max:]...)
	}
}

// snapshot returns a copy of the current contents (oldest first).
func (r *ringBuffer) snapshot() []byte {
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out
}
