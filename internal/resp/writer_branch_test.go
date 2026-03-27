// Precise underlying-write failure tests for Writer (bufio size 1).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package resp

import (
	"fmt"
	"testing"
)

// failAfterUnderlingWrites fails the Nth write to the underlying io.Writer (each Write call counts as one).
type failAfterUnderlyingWrites struct {
	n   int
	max int
	err error
}

func (f *failAfterUnderlyingWrites) Write(p []byte) (int, error) {
	f.n++
	if f.n > f.max {
		return 0, f.err
	}
	return len(p), nil
}

func TestWriteSimpleStringErrorsEachStage(t *testing.T) {
	e := fmt.Errorf("boom")
	w := newWriterBuf1(&failAfterUnderlyingWrites{max: 0, err: e})
	if err := w.WriteSimpleString("OK"); err == nil {
		t.Fatal("expected error at first byte")
	}
	w = newWriterBuf1(&failAfterUnderlyingWrites{max: 1, err: e})
	if err := w.WriteSimpleString("OK"); err == nil {
		t.Fatal("expected error at second stage")
	}
	w = newWriterBuf1(&failAfterUnderlyingWrites{max: 2, err: e})
	if err := w.WriteSimpleString("OK"); err == nil {
		t.Fatal("expected error at third stage")
	}
}

func TestWriteErrorAllFailureStages(t *testing.T) {
	e := fmt.Errorf("boom")
	for _, max := range []int{0, 1, 2, 3} {
		w := newWriterBuf1(&failAfterUnderlyingWrites{max: max, err: e})
		if err := w.WriteError("ab"); err == nil {
			t.Fatalf("max=%d expected error", max)
		}
	}
}

func TestWriteIntegerAllFailureStages(t *testing.T) {
	e := fmt.Errorf("boom")
	for _, max := range []int{0, 1, 2, 3} {
		w := newWriterBuf1(&failAfterUnderlyingWrites{max: max, err: e})
		if err := w.WriteInteger(42); err == nil {
			t.Fatalf("max=%d expected error", max)
		}
	}
}

func TestWriteBulkBytesPayloadError(t *testing.T) {
	e := fmt.Errorf("boom")
	w := newWriterBuf1(&failAfterUnderlyingWrites{max: 2, err: e})
	if err := w.WriteBulkString([]byte("ab")); err == nil {
		t.Fatal("expected error during payload")
	}
}

func TestWriteArrayElementWriteError(t *testing.T) {
	e := fmt.Errorf("boom")
	w := newWriterBuf1(&failAfterUnderlyingWrites{max: 4, err: e})
	if err := w.WriteArray([]Value{{Type: ':', Integer: 1}, {Type: ':', Integer: 2}}); err == nil {
		t.Fatal("expected error on second element")
	}
}
