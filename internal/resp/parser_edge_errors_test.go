// Targeted parser error-path tests.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package resp

import (
	"io"
	"strings"
	"testing"
)

func TestParseIntegerEmptyAfterTrim(t *testing.T) {
	p := NewParser(strings.NewReader(":   \r\n"))
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "empty integer") {
		t.Fatalf("%v", err)
	}
}

func TestParseBulkInvalidNegativeLength(t *testing.T) {
	p := NewParser(strings.NewReader("$-3\r\n"))
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "invalid bulk length") {
		t.Fatalf("%v", err)
	}
}

func TestParseBulkLengthOverflowParseInt(t *testing.T) {
	p := NewParser(strings.NewReader("$9223372036854775808\r\n"))
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("%v", err)
	}
}

func TestParseArrayCountOverflowParseInt(t *testing.T) {
	p := NewParser(strings.NewReader("*9223372036854775808\r\n"))
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("%v", err)
	}
}

func TestParseArrayInvalidNegativeCount(t *testing.T) {
	p := NewParser(strings.NewReader("*-3\r\n"))
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "invalid array count") {
		t.Fatalf("%v", err)
	}
}

func TestParseArrayElementError(t *testing.T) {
	p := NewParser(strings.NewReader("*1\r\n%bad\r\n"))
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "element") {
		t.Fatalf("%v", err)
	}
}

func TestReadLineLongBufferWriteError(t *testing.T) {
	p := NewParser(io.MultiReader(strings.NewReader("+"), errReader{}))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestExpectCRLFEOF(t *testing.T) {
	p := NewParser(strings.NewReader("$1\r\na"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInlineEmptyAfterTrim(t *testing.T) {
	p := NewParser(strings.NewReader("   \r\n"))
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "empty inline") {
		t.Fatalf("%v", err)
	}
}
