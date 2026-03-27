// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// RESP response writer for Super-Cache.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package resp

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
)

// Writer encodes RESP2 values to an io.Writer using an internal buffer.
type Writer struct {
	bw *bufio.Writer
	uw io.Writer
}

// NewWriter returns a RESP writer that buffers output to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{
		bw: bufio.NewWriterSize(w, 64*1024),
		uw: w,
	}
}

// WriteSimpleString writes a RESP simple string (+...\r\n).
func (w *Writer) WriteSimpleString(s string) error {
	if err := w.bw.WriteByte('+'); err != nil {
		return fmt.Errorf("write simple string: %w", err)
	}
	if _, err := w.bw.WriteString(s); err != nil {
		return fmt.Errorf("write simple string: %w", err)
	}
	if _, err := w.bw.WriteString("\r\n"); err != nil {
		return fmt.Errorf("write simple string: %w", err)
	}
	return nil
}

// WriteError writes a RESP error (-...\r\n).
func (w *Writer) WriteError(s string) error {
	if err := w.bw.WriteByte('-'); err != nil {
		return fmt.Errorf("write error: %w", err)
	}
	if _, err := w.bw.WriteString(s); err != nil {
		return fmt.Errorf("write error: %w", err)
	}
	if _, err := w.bw.WriteString("\r\n"); err != nil {
		return fmt.Errorf("write error: %w", err)
	}
	return nil
}

// WriteInteger writes a RESP integer (:n\r\n).
func (w *Writer) WriteInteger(n int64) error {
	if _, err := w.bw.WriteString(":"); err != nil {
		return fmt.Errorf("write integer: %w", err)
	}
	if _, err := w.bw.WriteString(strconv.FormatInt(n, 10)); err != nil {
		return fmt.Errorf("write integer: %w", err)
	}
	if _, err := w.bw.WriteString("\r\n"); err != nil {
		return fmt.Errorf("write integer: %w", err)
	}
	return nil
}

// WriteBulkString writes a RESP bulk string ($len\r\npayload\r\n).
func (w *Writer) WriteBulkString(b []byte) error {
	if err := w.writeBulkBytes(b); err != nil {
		return fmt.Errorf("write bulk string: %w", err)
	}
	return nil
}

func (w *Writer) writeBulkBytes(b []byte) error {
	if _, err := fmt.Fprintf(w.bw, "$%d\r\n", len(b)); err != nil {
		return fmt.Errorf("write bulk header: %w", err)
	}
	if len(b) > 0 {
		if _, err := w.bw.Write(b); err != nil {
			return fmt.Errorf("write bulk payload: %w", err)
		}
	}
	if _, err := w.bw.WriteString("\r\n"); err != nil {
		return fmt.Errorf("write bulk trailer: %w", err)
	}
	return nil
}

// WriteNullBulkString writes a null bulk string ($-1\r\n).
func (w *Writer) WriteNullBulkString() error {
	if _, err := w.bw.WriteString("$-1\r\n"); err != nil {
		return fmt.Errorf("write null bulk: %w", err)
	}
	return nil
}

// WriteArray writes a RESP array by recursively encoding elements.
func (w *Writer) WriteArray(elems []Value) error {
	if err := w.writeArrayHeader(len(elems)); err != nil {
		return fmt.Errorf("write array: %w", err)
	}
	for i := range elems {
		if err := w.WriteValue(elems[i]); err != nil {
			return fmt.Errorf("write array element %d: %w", i, err)
		}
	}
	return nil
}

func (w *Writer) writeArrayHeader(n int) error {
	if _, err := fmt.Fprintf(w.bw, "*%d\r\n", n); err != nil {
		return fmt.Errorf("write array header: %w", err)
	}
	return nil
}

// WriteNull writes a null RESP array (*-1\r\n).
func (w *Writer) WriteNull() error {
	if _, err := w.bw.WriteString("*-1\r\n"); err != nil {
		return fmt.Errorf("write null array: %w", err)
	}
	return nil
}

// WriteValue encodes any Value, including nested arrays.
func (w *Writer) WriteValue(v Value) error {
	switch v.Type {
	case '+':
		return w.WriteSimpleString(v.Str)
	case '-':
		return w.WriteError(v.Str)
	case ':':
		return w.WriteInteger(v.Integer)
	case '$':
		if v.IsNull {
			return w.WriteNullBulkString()
		}
		return w.WriteBulkString([]byte(v.Str))
	case '*':
		if v.IsNull {
			return w.WriteNull()
		}
		return w.WriteArray(v.Array)
	default:
		return fmt.Errorf("unknown value type %q", v.Type)
	}
}

// Flush flushes the buffered writer and, if the underlying writer implements io.Flusher, flushes it.
func (w *Writer) Flush() error {
	if err := w.bw.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	if f, ok := w.uw.(interface{ Flush() error }); ok {
		if err := f.Flush(); err != nil {
			return fmt.Errorf("flush underlying: %w", err)
		}
	}
	return nil
}
