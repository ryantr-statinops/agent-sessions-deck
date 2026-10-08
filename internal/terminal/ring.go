package terminal

// byteRing is a fixed-capacity FIFO. Its backing allocation never grows.
type byteRing struct {
	buffer []byte
	head   int
	size   int
}

func newByteRing(capacity int) byteRing {
	return byteRing{buffer: make([]byte, capacity)}
}

func (r *byteRing) Len() int { return r.size }

func (r *byteRing) Free() int { return len(r.buffer) - r.size }

// Write appends all bytes or leaves the ring unchanged when capacity is short.
func (r *byteRing) Write(p []byte) bool {
	if len(p) > r.Free() {
		return false
	}
	if len(p) == 0 {
		return true
	}
	tail := (r.head + r.size) % len(r.buffer)
	first := copy(r.buffer[tail:], p)
	copy(r.buffer, p[first:])
	r.size += len(p)
	return true
}

// Read removes up to len(p) bytes and returns the number copied.
func (r *byteRing) Read(p []byte) int {
	if len(p) == 0 || r.size == 0 {
		return 0
	}
	n := min(len(p), r.size)
	first := min(n, len(r.buffer)-r.head)
	copy(p, r.buffer[r.head:r.head+first])
	copy(p[first:n], r.buffer[:n-first])
	r.head = (r.head + n) % len(r.buffer)
	r.size -= n
	if r.size == 0 {
		r.head = 0
	}
	return n
}

func (r *byteRing) Clear() {
	r.head = 0
	r.size = 0
}
