// Parser edge cases for coverage (bulk pools, arrays, idle noise).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package resp

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSkipIdleNoiseMultiple(t *testing.T) {
	p := NewParser(strings.NewReader("\r\n\n+P\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Str != "P" {
		t.Fatal(v)
	}
}

func TestParseBulkOver4096NoPoolReturn(t *testing.T) {
	var b strings.Builder
	b.WriteString("$5000\r\n")
	for i := 0; i < 5000; i++ {
		b.WriteByte('z')
	}
	b.WriteString("\r\n")
	p := NewParser(strings.NewReader(b.String()))
	v, err := p.Parse()
	if err != nil || len(v.Str) != 5000 {
		t.Fatalf("%v %+v", err, v)
	}
}

func TestParseArrayCountTooLarge(t *testing.T) {
	p := NewParser(strings.NewReader("*1073741825\r\n"))
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("got %v", err)
	}
}

func TestParseArrayCountInvalidNegative(t *testing.T) {
	p := NewParser(strings.NewReader("*-2\r\n"))
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "invalid array count") {
		t.Fatalf("got %v", err)
	}
}

func TestParseBulkLengthInvalidBelowMinusOne(t *testing.T) {
	p := NewParser(strings.NewReader("$-2\r\n"))
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "invalid bulk length") {
		t.Fatalf("got %v", err)
	}
}

func TestParseBulkLengthParseIntNonRangeError(t *testing.T) {
	p := NewParser(strings.NewReader("$abc\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExpectCRLFWrongBytes(t *testing.T) {
	p := NewParser(strings.NewReader("$2\r\nabxx"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBulkPoolReturnOnTruncatedCRLF(t *testing.T) {
	// Small bulk uses pool; malformed trailer must return pooled buffer (parseBulkString error path).
	p := NewParser(strings.NewReader("$1\r\nx\r"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseArrayCountLineEmptyAfterTrim(t *testing.T) {
	p := NewParser(strings.NewReader("*  \r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseBulkUnreadEOF(t *testing.T) {
	p := NewParser(strings.NewReader("$1\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "read") {
		t.Fatalf("%v", err)
	}
}

func TestSkipIdleNoiseCRWithoutLF(t *testing.T) {
	p := NewParser(strings.NewReader("\rX+P\r\n"))
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "LF") {
		t.Fatalf("got %v", err)
	}
}

func TestSkipIdleNoisePeekNonEOFError(t *testing.T) {
	p := NewParser(peekFailReader{})
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "peek") {
		t.Fatalf("got %v", err)
	}
}

type peekFailReader struct{}

func (peekFailReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestParseInlineFieldsEmptyAfterSplit(t *testing.T) {
	p := NewParser(strings.NewReader("   foo   \r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Array) != 1 || v.Array[0].Str != "foo" {
		t.Fatalf("%+v", v)
	}
}

func TestWriteValueEveryType(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	cases := []Value{
		{Type: '+', Str: "a"},
		{Type: '-', Str: "b"},
		{Type: ':', Integer: 3},
		{Type: '$', Str: "xy", IsNull: false},
		{Type: '$', IsNull: true},
		{Type: '*', Array: []Value{{Type: ':', Integer: 9}}},
		{Type: '*', IsNull: true},
	}
	for _, v := range cases {
		if err := w.WriteValue(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}
