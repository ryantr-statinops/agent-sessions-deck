//go:build linux

package cli

import (
	"bytes"
	"errors"
	"io"
)

const detachByte = 0x1d // Ctrl+]

// copyTerminalInput forwards raw input until stdin closes or the detach byte is pressed.
func copyTerminalInput(source io.Reader, target io.Writer) error {
	var buffer [32 << 10]byte
	for {
		n, readErr := source.Read(buffer[:])
		if n > 0 {
			chunk := buffer[:n]
			if detach := bytes.IndexByte(chunk, detachByte); detach >= 0 {
				if detach > 0 {
					written, err := target.Write(chunk[:detach])
					if err != nil {
						return err
					}
					if written != detach {
						return io.ErrShortWrite
					}
				}
				return nil
			}
			written, err := target.Write(chunk)
			if err != nil {
				return err
			}
			if written != len(chunk) {
				return io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
		if n == 0 {
			continue
		}
	}
}
