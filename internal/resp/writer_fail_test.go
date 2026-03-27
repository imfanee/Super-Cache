// Error-path coverage for Writer using failing io.Writers.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package resp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"testing"
)

// newWriterBuf1 returns a Writer with a 1-byte bufio buffer so each write hits the underlying writer quickly.
func newWriterBuf1(w io.Writer) *Writer {
	return &Writer{bw: bufio.NewWriterSize(w, 1), uw: w}
}

type failOnSecondWrite struct {
	n   int
	err error
}

func (f *failOnSecondWrite) Write(p []byte) (int, error) {
	f.n++
	if f.n >= 2 {
		return 0, f.err
	}
	return len(p), nil
}

func TestWriterErrorsAfterBufferedFlushes(t *testing.T) {
	e := fmt.Errorf("boom")
	u := &failOnSecondWrite{err: e}
	w := newWriterBuf1(u)
	if err := w.WriteNullBulkString(); err == nil {
		t.Fatal("expected error")
	}
	u = &failOnSecondWrite{err: e}
	w = newWriterBuf1(u)
	if err := w.WriteNull(); err == nil {
		t.Fatal("expected error")
	}
}

type alwaysErr struct{ err error }

func (alwaysErr) Write([]byte) (int, error) { return 0, fmt.Errorf("always") }

func TestWriterMethodsPropagateUnderlyingErrors(t *testing.T) {
	e := fmt.Errorf("underlying")
	ae := alwaysErr{err: e}
	check := func(name string, fn func(*Writer) error) {
		t.Helper()
		w := newWriterBuf1(ae)
		if err := fn(w); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
	check("WriteSimpleString", func(w *Writer) error { return w.WriteSimpleString("OK") })
	check("WriteError", func(w *Writer) error { return w.WriteError("E") })
	check("WriteInteger", func(w *Writer) error { return w.WriteInteger(1) })
	check("WriteBulkString", func(w *Writer) error { return w.WriteBulkString([]byte("ab")) })
	check("WriteArray", func(w *Writer) error {
		return w.WriteArray([]Value{{Type: ':', Integer: 1}})
	})
	check("writeArrayHeader", func(w *Writer) error { return w.writeArrayHeader(2) })
}

type errFlushBuf struct{ bytes.Buffer }

func (*errFlushBuf) Flush() error { return fmt.Errorf("underlying flush") }

func TestFlushUnderlyingFlusherError(t *testing.T) {
	w := NewWriter(&errFlushBuf{})
	if err := w.WriteSimpleString("OK"); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err == nil {
		t.Fatal("expected flush error")
	}
}
