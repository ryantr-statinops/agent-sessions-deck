//go:build linux

package control

import (
	"errors"
	"io"
	"testing"
)

type readStep struct {
	data []byte
	err  error
}

type stepReader struct {
	steps []readStep
}

func (r *stepReader) Read(dst []byte) (int, error) {
	if len(r.steps) == 0 {
		return 0, io.EOF
	}
	step := r.steps[0]
	r.steps = r.steps[1:]
	return copy(dst, step.data), step.err
}

func TestReaderResumesPartialRecordAfterTimeout(t *testing.T) {
	timeout := errors.New("read deadline exceeded")
	reader := NewReader(&stepReader{steps: []readStep{
		{data: []byte(`{"event":`), err: timeout},
		{data: []byte(`"ready"}` + "\n")},
	}})

	if _, err := reader.Read(); !errors.Is(err, timeout) {
		t.Fatalf("first read error = %v, want timeout", err)
	}
	got, err := reader.Read()
	if err != nil {
		t.Fatalf("read after timeout: %v", err)
	}
	if string(got) != `{"event":"ready"}` {
		t.Fatalf("record after timeout = %q, want complete JSON record", got)
	}
}
