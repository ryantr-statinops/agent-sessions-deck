//go:build linux

package cli

import (
	"bytes"
	"testing"
)

func TestDetachByteIsNotForwardedAndFollowingInputIsDropped(t *testing.T) {
	var output bytes.Buffer
	input := bytes.NewReader([]byte{'a', 'b', detachByte, 'c', 'd'})
	if err := copyTerminalInput(input, &output); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "ab" {
		t.Fatalf("forwarded bytes = %q, want pre-detach bytes only", got)
	}
}

func TestTerminalInputCopiesOrdinaryBytesUntilEOF(t *testing.T) {
	var output bytes.Buffer
	const input = "literal argv bytes\x00"
	if err := copyTerminalInput(bytes.NewBufferString(input), &output); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != input {
		t.Fatalf("forwarded bytes = %q, want %q", got, input)
	}
}
