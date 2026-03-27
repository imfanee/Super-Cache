// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Package resp implements Redis RESP2 parsing and writing.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package resp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

// MaxBulkBytes is the maximum allowed bulk string payload size (512 MiB).
const MaxBulkBytes = 512 * 1024 * 1024

// MaxArrayElements is the maximum number of elements in a RESP array.
const MaxArrayElements = 1 << 20 // ~1 million

// maxLineBytes caps the total length of a simple/error/integer RESP line to prevent OOM.
const maxLineBytes = 64 * 1024

// bulkBodyPool recycles small bulk-string read buffers (initial cap 512; only slices <= 4096 bytes returned).
var bulkBodyPool = sync.Pool{
	New: func() any {
		b := make([]byte, 512)
		return &b
	},
}

func byteString(b []byte) string {
	// unsafe.String(nil, 0) is valid for an empty slice (returns "").
	return unsafe.String(unsafe.SliceData(b), len(b))
}

func trimSpaceBytes(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	for i < j && (b[j-1] == ' ' || b[j-1] == '\t') {
		j--
	}
	return b[i:j]
}

// Parser decodes RESP2 (and inline) protocol from a byte stream.
type Parser struct {
	br *bufio.Reader
}

// NewParser returns a RESP parser that reads from r using an internal buffer.
// Never uses io.ReadAll on the underlying connection; data is read incrementally.
func NewParser(r io.Reader) *Parser {
	return &Parser{br: bufio.NewReaderSize(r, 64*1024)}
}

// Reset discards buffered input and switches the parser to read from r.
// Callers may reuse a Parser across sequential command streams (e.g. after bytes.Reader.Reset).
func (p *Parser) Reset(r io.Reader) {
	p.br.Reset(r)
}

// Parse reads exactly one RESP value or one inline command from the stream.
// Pipelined commands are consumed one per call; buffered input is preserved across calls.
func (p *Parser) Parse() (Value, error) {
	if err := p.skipIdleNoise(); err != nil {
		return Value{}, err
	}
	b, err := p.br.ReadByte()
	if err != nil {
		return Value{}, fmt.Errorf("read first byte: %w", err)
	}
	switch b {
	case '+', '-', ':', '$', '*':
		// UnreadByte cannot fail here: it immediately follows a successful ReadByte on the same bufio.Reader.
		_ = p.br.UnreadByte()
		return p.parseRESPValue()
	default:
		_ = p.br.UnreadByte()
		return p.parseInlineCommand()
	}
}

func (p *Parser) skipIdleNoise() error {
	for {
		bs, err := p.br.Peek(1)
		if err != nil {
			if err == io.EOF {
				return io.EOF
			}
			return fmt.Errorf("peek: %w", err)
		}
		switch bs[0] {
		case '\r':
			if _, err := p.br.ReadByte(); err != nil {
				return fmt.Errorf("skip crlf: %w", err)
			}
			nl, err := p.br.ReadByte()
			if err != nil {
				return fmt.Errorf("skip crlf: %w", err)
			}
			if nl != '\n' {
				return fmt.Errorf("expected LF after CR")
			}
		case '\n':
			if _, err := p.br.ReadByte(); err != nil {
				return fmt.Errorf("skip lf: %w", err)
			}
		default:
			return nil
		}
	}
}

func (p *Parser) parseRESPValue() (Value, error) {
	typ, err := p.br.ReadByte()
	if err != nil {
		return Value{}, fmt.Errorf("read type: %w", err)
	}
	switch typ {
	case '+':
		return p.parseSimpleStringValue()
	case '-':
		return p.parseErrorValue()
	case ':':
		return p.parseIntegerValue()
	case '$':
		return p.parseBulkString()
	case '*':
		return p.parseArray()
	default:
		return Value{}, fmt.Errorf("invalid RESP type byte %q", typ)
	}
}

func (p *Parser) parseSimpleStringValue() (Value, error) {
	line, err := p.readLineBytes()
	if err != nil {
		return Value{}, fmt.Errorf("simple string: %w", err)
	}
	if len(line) > 65536 {
		return Value{}, fmt.Errorf("simple string: line too long")
	}
	return Value{Type: '+', Str: string(line)}, nil
}

func (p *Parser) parseErrorValue() (Value, error) {
	line, err := p.readLineBytes()
	if err != nil {
		return Value{}, fmt.Errorf("error string: %w", err)
	}
	if len(line) > maxLineBytes {
		return Value{}, fmt.Errorf("error string: line too long")
	}
	return Value{Type: '-', Str: string(line)}, nil
}

func (p *Parser) parseIntegerValue() (Value, error) {
	line, err := p.readLineBytes()
	if err != nil {
		return Value{}, fmt.Errorf("integer line: %w", err)
	}
	t := trimSpaceBytes(line)
	if len(t) == 0 {
		return Value{}, fmt.Errorf("empty integer")
	}
	n, err := strconv.ParseInt(byteString(t), 10, 64)
	if err != nil {
		var numErr *strconv.NumError
		if errors.As(err, &numErr) && numErr.Err == strconv.ErrRange {
			return Value{}, fmt.Errorf("integer overflow: %w", err)
		}
		return Value{}, fmt.Errorf("parse integer: %w", err)
	}
	return Value{Type: ':', Integer: n}, nil
}

func (p *Parser) parseBulkString() (Value, error) {
	line, err := p.readLineBytes()
	if err != nil {
		return Value{}, fmt.Errorf("bulk length line: %w", err)
	}
	t := trimSpaceBytes(line)
	if len(t) == 0 {
		return Value{}, fmt.Errorf("empty bulk length")
	}
	n64, err := strconv.ParseInt(byteString(t), 10, 64)
	if err != nil {
		var numErr *strconv.NumError
		if errors.As(err, &numErr) && numErr.Err == strconv.ErrRange {
			return Value{}, fmt.Errorf("bulk length overflow: %w", err)
		}
		return Value{}, fmt.Errorf("parse bulk length: %w", err)
	}
	if n64 < -1 {
		return Value{}, fmt.Errorf("invalid bulk length %d", n64)
	}
	if n64 == -1 {
		return Value{Type: '$', IsNull: true}, nil
	}
	if n64 > MaxBulkBytes {
		return Value{}, fmt.Errorf("ERR Protocol error: invalid bulk length")
	}
	n := int(n64)
	var body []byte
	var slot *[]byte
	if n <= 4096 {
		slot = bulkBodyPool.Get().(*[]byte)
		buf := *slot
		if cap(buf) < n {
			bulkBodyPool.Put(slot)
			slot = nil
			body = make([]byte, n)
		} else {
			body = buf[:n]
		}
	} else {
		body = make([]byte, n)
	}
	if _, err := io.ReadFull(p.br, body); err != nil {
		if slot != nil {
			*slot = (*slot)[:0]
			bulkBodyPool.Put(slot)
		}
		return Value{}, fmt.Errorf("read bulk body: %w", err)
	}
	if err := p.expectCRLF(); err != nil {
		if slot != nil {
			*slot = (*slot)[:0]
			bulkBodyPool.Put(slot)
		}
		return Value{}, err
	}
	v := Value{Type: '$', Str: string(body), IsNull: false}
	if slot != nil && cap(body) <= 4096 {
		*slot = body[:0]
		bulkBodyPool.Put(slot)
	}
	return v, nil
}

func (p *Parser) parseArray() (Value, error) {
	line, err := p.readLineBytes()
	if err != nil {
		return Value{}, fmt.Errorf("array count line: %w", err)
	}
	t := trimSpaceBytes(line)
	if len(t) == 0 {
		return Value{}, fmt.Errorf("empty array count")
	}
	n64, err := strconv.ParseInt(byteString(t), 10, 64)
	if err != nil {
		var numErr *strconv.NumError
		if errors.As(err, &numErr) && numErr.Err == strconv.ErrRange {
			return Value{}, fmt.Errorf("array count overflow: %w", err)
		}
		return Value{}, fmt.Errorf("parse array count: %w", err)
	}
	if n64 < -1 {
		return Value{}, fmt.Errorf("invalid array count %d", n64)
	}
	if n64 == -1 {
		return Value{Type: '*', IsNull: true, Array: nil}, nil
	}
	if n64 > MaxArrayElements {
		return Value{}, fmt.Errorf("array count too large")
	}
	n := int(n64)
	out := make([]Value, 0, n)
	for i := 0; i < n; i++ {
		v, err := p.parseRESPValue()
		if err != nil {
			return Value{}, fmt.Errorf("array element %d: %w", i, err)
		}
		out = append(out, v)
	}
	return Value{Type: '*', Array: out, IsNull: false}, nil
}

func (p *Parser) parseInlineCommand() (Value, error) {
	line, err := p.readLineBytes()
	if err != nil {
		return Value{}, fmt.Errorf("inline line: %w", err)
	}
	s := strings.TrimSpace(string(line))
	if s == "" {
		return Value{}, fmt.Errorf("empty inline command")
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return Value{}, fmt.Errorf("empty inline command")
	}
	arr := make([]Value, len(fields))
	for i, f := range fields {
		arr[i] = Value{Type: '$', Str: f, IsNull: false}
	}
	return Value{Type: '*', Array: arr, IsNull: false}, nil
}

func (p *Parser) readLineBytes() ([]byte, error) {
	var stack [4096]byte
	pos := 0
	for {
		b, err := p.br.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("read line: %w", err)
		}
		if b == '\r' {
			nl, err := p.br.ReadByte()
			if err != nil {
				return nil, fmt.Errorf("read line: %w", err)
			}
			if nl != '\n' {
				return nil, fmt.Errorf("expected LF after CR")
			}
			return append([]byte(nil), stack[:pos]...), nil
		}
		if pos >= len(stack) {
			var buf bytes.Buffer
			buf.Write(stack[:])
			if err := buf.WriteByte(b); err != nil {
				return nil, fmt.Errorf("buffer line: %w", err)
			}
			for {
				if buf.Len() > maxLineBytes {
					return nil, fmt.Errorf("line too long (exceeds %d bytes)", maxLineBytes)
				}
				b2, err := p.br.ReadByte()
				if err != nil {
					return nil, fmt.Errorf("read line: %w", err)
				}
				if b2 == '\r' {
					nl, err := p.br.ReadByte()
					if err != nil {
						return nil, fmt.Errorf("read line: %w", err)
					}
					if nl != '\n' {
						return nil, fmt.Errorf("expected LF after CR")
					}
					return buf.Bytes(), nil
				}
				if err := buf.WriteByte(b2); err != nil {
					return nil, fmt.Errorf("buffer line: %w", err)
				}
			}
		}
		stack[pos] = b
		pos++
	}
}

func (p *Parser) expectCRLF() error {
	cr, err := p.br.ReadByte()
	if err != nil {
		return fmt.Errorf("expect CRLF: %w", err)
	}
	lf, err := p.br.ReadByte()
	if err != nil {
		return fmt.Errorf("expect CRLF: %w", err)
	}
	if cr != '\r' || lf != '\n' {
		return fmt.Errorf("expected CRLF after bulk")
	}
	return nil
}
