// Exact RESP framing tests for Writer.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package resp

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteExactFrames(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	_ = w.WriteSimpleString("OK")
	_ = w.WriteError("ERR something")
	_ = w.WriteInteger(42)
	_ = w.WriteBulkString([]byte("foo"))
	_ = w.WriteNullBulkString()
	_ = w.writeArrayHeader(3)
	_ = w.WriteNull()
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	want := "+OK\r\n-ERR something\r\n:42\r\n$3\r\nfoo\r\n$-1\r\n*3\r\n*-1\r\n"
	if buf.String() != want {
		t.Fatalf("got %q want %q", buf.String(), want)
	}
}

func TestInlineSetFooBar(t *testing.T) {
	p := NewParser(bytes.NewReader([]byte("SET foo bar\r\n")))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != '*' || len(v.Array) != 3 {
		t.Fatalf("%+v", v)
	}
	if v.Array[0].Str != "SET" || v.Array[1].Str != "foo" || v.Array[2].Str != "bar" {
		t.Fatalf("%+v", v.Array)
	}
}

func TestReadLineBytesLongLine(t *testing.T) {
	var b strings.Builder
	b.WriteByte('+')
	for i := 0; i < 5000; i++ {
		b.WriteByte('x')
	}
	b.WriteString("\r\n")
	p := NewParser(strings.NewReader(b.String()))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != '+' || len(v.Str) != 5000 {
		t.Fatalf("len %d %+v", len(v.Str), v)
	}
}

func TestParserReset(t *testing.T) {
	data := []byte("+P\r\n")
	r := bytes.NewReader(data)
	p := NewParser(r)
	r.Reset(data)
	p.Reset(r)
	v, err := p.Parse()
	if err != nil || v.Str != "P" {
		t.Fatalf("%+v %v", v, err)
	}
}
