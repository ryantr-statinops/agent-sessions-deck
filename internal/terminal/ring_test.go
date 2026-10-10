package terminal

import "testing"

func TestByteRingPreservesFIFOAcrossWraparound(t *testing.T) {
	ring := newByteRing(5)
	if !ring.Write([]byte("abcde")) {
		t.Fatal("Write() rejected data that fits")
	}
	var first [2]byte
	if n := ring.Read(first[:]); n != len(first) || string(first[:]) != "ab" {
		t.Fatalf("first Read() = %q (%d), want ab (2)", first[:], n)
	}
	if !ring.Write([]byte("fg")) {
		t.Fatal("Write() rejected wrapped data that fits")
	}
	var rest [5]byte
	if n := ring.Read(rest[:]); n != len(rest) || string(rest[:]) != "cdefg" {
		t.Fatalf("wrapped Read() = %q (%d), want cdefg (5)", rest[:], n)
	}
	if ring.Len() != 0 || ring.Free() != 5 {
		t.Fatalf("empty ring len/free = %d/%d, want 0/5", ring.Len(), ring.Free())
	}
}

func TestByteRingOverflowIsAtomic(t *testing.T) {
	ring := newByteRing(4)
	if !ring.Write([]byte("abc")) {
		t.Fatal("initial Write() failed")
	}
	if ring.Write([]byte("de")) {
		t.Fatal("Write() accepted data beyond capacity")
	}
	var got [4]byte
	if n := ring.Read(got[:]); n != 3 || string(got[:n]) != "abc" {
		t.Fatalf("Read() after rejected overflow = %q (%d), want abc (3)", got[:n], n)
	}
	if !ring.Write([]byte("d")) {
		t.Fatal("Write() failed after space was freed")
	}
	if n := ring.Read(got[:]); n != 1 || string(got[:n]) != "d" {
		t.Fatalf("Read() after refill = %q (%d), want d (1)", got[:n], n)
	}
}

func TestByteRingClearDropsQueuedBytes(t *testing.T) {
	ring := newByteRing(4)
	if !ring.Write([]byte("data")) {
		t.Fatal("Write() failed")
	}
	ring.Clear()
	if ring.Len() != 0 || ring.Free() != 4 {
		t.Fatalf("cleared ring len/free = %d/%d, want 0/4", ring.Len(), ring.Free())
	}
}
