// Fuzz tests for RESP parser robustness and coverage.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package resp

import (
	"bytes"
	"testing"
)

func FuzzParse(f *testing.F) {
	f.Add([]byte("+OK\r\n"))
	f.Add([]byte("*0\r\n"))
	f.Add([]byte("$-1\r\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		p := NewParser(bytes.NewReader(data))
		_, _ = p.Parse()
	})
}
