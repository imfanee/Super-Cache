// Tests for RESP parsing and writing.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package resp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
)

func TestEmptyBulkString(t *testing.T) {
	p := NewParser(strings.NewReader("$0\r\n\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != '$' || v.IsNull || v.Str != "" {
		t.Fatalf("got %+v", v)
	}
}

func TestNullBulkString(t *testing.T) {
	p := NewParser(strings.NewReader("$-1\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != '$' || !v.IsNull {
		t.Fatalf("got %+v", v)
	}
}

func TestNullArray(t *testing.T) {
	p := NewParser(strings.NewReader("*-1\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != '*' || !v.IsNull || v.Array != nil {
		t.Fatalf("got %+v", v)
	}
}

func TestEmptyArray(t *testing.T) {
	p := NewParser(strings.NewReader("*0\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != '*' || v.IsNull || len(v.Array) != 0 {
		t.Fatalf("got %+v", v)
	}
}

func TestNestedArrays(t *testing.T) {
	in := "*2\r\n*2\r\n$1\r\na\r\n$1\r\nb\r\n*1\r\n$1\r\nz\r\n"
	p := NewParser(strings.NewReader(in))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != '*' || len(v.Array) != 2 {
		t.Fatalf("root %+v", v)
	}
	if v.Array[0].Type != '*' || len(v.Array[0].Array) != 2 {
		t.Fatalf("nested0 %+v", v.Array[0])
	}
	if v.Array[1].Type != '*' || len(v.Array[1].Array) != 1 {
		t.Fatalf("nested1 %+v", v.Array[1])
	}
}

func TestIntegerOverflow(t *testing.T) {
	p := NewParser(strings.NewReader(":9223372036854775808\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected overflow error")
	}
	if !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("got %v", err)
	}
}

func TestBulkLengthOverflowInt64(t *testing.T) {
	p := NewParser(strings.NewReader("$9223372036854775808\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBulkTooLarge(t *testing.T) {
	n := strconv.FormatInt(MaxBulkBytes+1, 10)
	p := NewParser(strings.NewReader("$" + n + "\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ERR Protocol error: invalid bulk length") {
		t.Fatalf("got %v", err)
	}
}

func TestPartialReads(t *testing.T) {
	data := []byte("*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n")
	r := &oneByteReader{data: data}
	p := NewParser(r)
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != '*' || len(v.Array) != 2 || v.Array[0].Str != "foo" || v.Array[1].Str != "bar" {
		t.Fatalf("got %+v", v)
	}
}

func TestInlineCommand(t *testing.T) {
	p := NewParser(strings.NewReader("PING\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != '*' || len(v.Array) != 1 || v.Array[0].Str != "PING" {
		t.Fatalf("got %+v", v)
	}
}

func TestInlineGET(t *testing.T) {
	p := NewParser(strings.NewReader("GET mykey\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Array) != 2 || v.Array[0].Str != "GET" || v.Array[1].Str != "mykey" {
		t.Fatalf("got %+v", v)
	}
}

func TestPipelining(t *testing.T) {
	p := NewParser(strings.NewReader("+OK\r\n+PONG\r\n"))
	v1, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v1.Str != "OK" {
		t.Fatal(v1)
	}
	v2, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v2.Str != "PONG" {
		t.Fatal(v2)
	}
}

func TestSkipLeadingCRLF(t *testing.T) {
	p := NewParser(strings.NewReader("\r\n+PONG\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != '+' || v.Str != "PONG" {
		t.Fatalf("%+v", v)
	}
}

func TestWriterRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteSimpleString("OK"); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	p := NewParser(&buf)
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != '+' || v.Str != "OK" {
		t.Fatal(v)
	}
}

func TestWriteValueArray(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	v := Value{Type: '*', Array: []Value{
		{Type: '$', Str: "GET"},
		{Type: '$', Str: "k"},
	}}
	if err := w.WriteValue(v); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	p := NewParser(&buf)
	out, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Array) != 2 {
		t.Fatal(out)
	}
}

func TestWriteNullBulkAndArray(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	_ = w.WriteNullBulkString()
	_ = w.WriteNull()
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	p := NewParser(&buf)
	v1, err := p.Parse()
	if err != nil || !v1.IsNull || v1.Type != '$' {
		t.Fatalf("%+v %v", v1, err)
	}
	v2, err := p.Parse()
	if err != nil || !v2.IsNull || v2.Type != '*' {
		t.Fatalf("%+v %v", v2, err)
	}
}

func TestParseErrorString(t *testing.T) {
	p := NewParser(strings.NewReader("-ERR nope\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != '-' || v.Str != "ERR nope" {
		t.Fatal(v)
	}
}

func TestParseInteger(t *testing.T) {
	p := NewParser(strings.NewReader(":42\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Integer != 42 || v.Type != ':' {
		t.Fatal(v)
	}
}

func TestParseIntegerNotNumeric(t *testing.T) {
	p := NewParser(strings.NewReader(":abc\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseBulkEmptyLengthLine(t *testing.T) {
	p := NewParser(strings.NewReader("$\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseArrayEmptyCountLine(t *testing.T) {
	p := NewParser(strings.NewReader("*\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseIntegerEmpty(t *testing.T) {
	p := NewParser(strings.NewReader(":\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEOF(t *testing.T) {
	p := NewParser(strings.NewReader(""))
	_, err := p.Parse()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("got %v", err)
	}
}

func TestBulkNegativeNotMinusOne(t *testing.T) {
	p := NewParser(strings.NewReader("$-2\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInvalidArrayCount(t *testing.T) {
	p := NewParser(strings.NewReader("*-2\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestArrayCountOverflow(t *testing.T) {
	p := NewParser(strings.NewReader("*9223372036854775808\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRESPInvalidType(t *testing.T) {
	p := NewParser(strings.NewReader("*1\r\n%bad\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteValueUnknownType(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	err := w.WriteValue(Value{Type: 'x'})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteBulkNilSlice(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteBulkString(nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	p := NewParser(&buf)
	v, err := p.Parse()
	if err != nil || v.Str != "" {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestFlushFlusherUnderlying(t *testing.T) {
	f := &flusherBuf{}
	w := NewWriter(f)
	if err := w.WriteSimpleString("x"); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if f.n == 0 {
		t.Fatal("expected underlying flush")
	}
}

func TestParseBulkTruncatedBody(t *testing.T) {
	p := NewParser(strings.NewReader("$5\r\nabc"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseArrayTruncated(t *testing.T) {
	p := NewParser(strings.NewReader("*1\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEmptyInlineRejected(t *testing.T) {
	p := NewParser(strings.NewReader("   \r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

// flusherBuf is a bytes.Buffer that counts Flush calls for testing.
type flusherBuf struct {
	bytes.Buffer
	n int
}

func (f *flusherBuf) Flush() error {
	f.n++
	return nil
}

func TestWriterMethodsCoverage(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteError("ERR x"); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteInteger(-42); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteBulkString([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteArray([]Value{{Type: '$', Str: "only"}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	p := NewParser(&buf)
	for i := 0; i < 4; i++ {
		if _, err := p.Parse(); err != nil {
			t.Fatalf("parse %d: %v", i, err)
		}
	}
}

func TestMalformedLineNoCRBeforeLF(t *testing.T) {
	p := NewParser(strings.NewReader("+bad\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBulkBadCRLF(t *testing.T) {
	p := NewParser(strings.NewReader("$3\r\nabc\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSkipIdleNoiseSingleLF(t *testing.T) {
	p := NewParser(strings.NewReader("\n+P\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Str != "P" {
		t.Fatal(v)
	}
}

func BenchmarkRESPParse(b *testing.B) {
	var bulk strings.Builder
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("k%05d", i) // fixed 6-byte key matches $6 bulk length
		bulk.WriteString("*3\r\n$3\r\nSET\r\n$6\r\n")
		bulk.WriteString(key)
		bulk.WriteString("\r\n$5\r\nvalue\r\n")
	}
	data := bulk.String()
	dataBytes := []byte(data)
	r := bytes.NewReader(dataBytes)
	p := NewParser(r)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Reset(dataBytes)
		p.Reset(r)
		for {
			_, err := p.Parse()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				b.Fatal(err)
			}
		}
	}
}

// oneByteReader returns one byte per Read to stress incremental buffering.
type oneByteReader struct {
	data []byte
	i    int
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.data) {
		return 0, io.EOF
	}
	p[0] = r.data[r.i]
	r.i++
	return 1, nil
}

// --- Coverage remediation: parser edge cases ---

func TestParseInvalidRESPTypeInArrayElement(t *testing.T) {
	p := NewParser(strings.NewReader("*1\r\n%\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error for invalid type in array")
	}
	if !strings.Contains(err.Error(), "invalid RESP type") {
		t.Fatalf("got %v", err)
	}
}

func TestSimpleStringTooLong(t *testing.T) {
	var b strings.Builder
	b.WriteByte('+')
	for i := 0; i < 65537; i++ {
		b.WriteByte('a')
	}
	b.WriteString("\r\n")
	p := NewParser(strings.NewReader(b.String()))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIntegerZero(t *testing.T) {
	p := NewParser(strings.NewReader(":0\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Type != ':' || v.Integer != 0 || v.IsNull {
		t.Fatalf("%+v", v)
	}
}

func TestIntegerNegative42(t *testing.T) {
	p := NewParser(strings.NewReader(":-42\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Integer != -42 {
		t.Fatal(v)
	}
}

func TestIntegerNonNumeric(t *testing.T) {
	p := NewParser(strings.NewReader(":1a2\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEmptyIntegerAfterTrim(t *testing.T) {
	p := NewParser(strings.NewReader(": \r\n"))
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "empty integer") {
		t.Fatalf("got %v", err)
	}
}

func TestEmptyBulkLengthLine(t *testing.T) {
	p := NewParser(strings.NewReader("$\r\n"))
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "empty bulk length") {
		t.Fatalf("got %v", err)
	}
}

func TestBulkStringOneByte(t *testing.T) {
	p := NewParser(strings.NewReader("$1\r\nx\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Str != "x" || v.Type != '$' {
		t.Fatal(v)
	}
}

func TestBulkString4096Bytes(t *testing.T) {
	payload := strings.Repeat("z", 4096)
	in := fmt.Sprintf("$4096\r\n%s\r\n", payload)
	p := NewParser(strings.NewReader(in))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Str) != 4096 {
		t.Fatalf("len %d", len(v.Str))
	}
}

func TestBulkStringStrictCRLF(t *testing.T) {
	p := NewParser(strings.NewReader("$3\r\nabc\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected strict CRLF error")
	}
}

func TestArrayOneBulkString(t *testing.T) {
	p := NewParser(strings.NewReader("*1\r\n$2\r\nhi\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Array) != 1 || v.Array[0].Str != "hi" {
		t.Fatalf("%+v", v)
	}
}

func TestArrayAlternatingSimpleAndInt(t *testing.T) {
	in := "*5\r\n+ok\r\n:1\r\n+no\r\n:2\r\n+yes\r\n"
	p := NewParser(strings.NewReader(in))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Array) != 5 {
		t.Fatal(len(v.Array))
	}
}

// errWriter always fails writes (used to force bufio flush errors with large payloads).
type errWriter struct {
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	return 0, e.err
}

func TestWriteSimpleStringUnderlyingError(t *testing.T) {
	uw := &errWriter{err: io.ErrClosedPipe}
	w := NewWriter(uw)
	if err := w.WriteSimpleString(strings.Repeat("x", 100000)); err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteErrorUnderlyingError(t *testing.T) {
	uw := &errWriter{err: io.ErrClosedPipe}
	w := NewWriter(uw)
	if err := w.WriteError(strings.Repeat("e", 100000)); err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteIntegerUnderlyingError(t *testing.T) {
	uw := &errWriter{err: io.ErrClosedPipe}
	w := NewWriter(uw)
	if err := w.WriteInteger(42); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err == nil {
		t.Fatal("expected flush error")
	}
}

func TestWriteBulkStringFailsBeforePayload(t *testing.T) {
	uw := &errWriter{err: io.ErrClosedPipe}
	w := NewWriter(uw)
	if err := w.WriteBulkString([]byte(strings.Repeat("a", 100000))); err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteArrayUnderlyingError(t *testing.T) {
	uw := &errWriter{err: io.ErrClosedPipe}
	w := NewWriter(uw)
	long := strings.Repeat("a", 100000)
	if err := w.WriteArray([]Value{{Type: '$', Str: long}}); err == nil {
		t.Fatal("expected error")
	}
}

type errFlushWriter struct {
	bytes.Buffer
	flushErr error
}

func (e *errFlushWriter) Flush() error {
	return e.flushErr
}

func TestFlushPropagatesUnderlyingFlushError(t *testing.T) {
	ef := &errFlushWriter{flushErr: io.ErrClosedPipe}
	w := NewWriter(ef)
	if err := w.WriteSimpleString("ok"); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err == nil {
		t.Fatal("expected flush error")
	}
}

func TestParseEOF(t *testing.T) {
	p := NewParser(strings.NewReader(""))
	_, err := p.Parse()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("got %v", err)
	}
}

func TestIntegerWithTrimSpace(t *testing.T) {
	p := NewParser(strings.NewReader(":\t 99 \t\r\n"))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if v.Integer != 99 {
		t.Fatal(v.Integer)
	}
}

func TestArrayCountTooLarge(t *testing.T) {
	p := NewParser(strings.NewReader("*1073741825\r\n"))
	_, err := p.Parse()
	if err == nil || !strings.Contains(err.Error(), "array count too large") {
		t.Fatalf("got %v", err)
	}
}

func TestSkipIdleNoiseMalformedCR(t *testing.T) {
	p := NewParser(strings.NewReader("\r"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteSimpleStringAndErrorSuccessPaths(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteSimpleString("ok"); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteError("e"); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestBulkString5000UsesHeapBody(t *testing.T) {
	body := strings.Repeat("b", 5000)
	p := NewParser(strings.NewReader(fmt.Sprintf("$5000\r\n%s\r\n", body)))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Str) != 5000 {
		t.Fatal(len(v.Str))
	}
}

func TestBulkString1000PoolReallocPath(t *testing.T) {
	// n<=4096 but larger than default pooled cap (512) exercises pool Put + heap alloc path.
	body := strings.Repeat("c", 1000)
	p := NewParser(strings.NewReader(fmt.Sprintf("$1000\r\n%s\r\n", body)))
	v, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Str) != 1000 {
		t.Fatal(len(v.Str))
	}
}

func TestParseNestedArrayElementError(t *testing.T) {
	p := NewParser(strings.NewReader("*1\r\n*1\r\n%\r\n"))
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error")
	}
}
