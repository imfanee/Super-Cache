// Stress-parse random inputs to exercise parser branches (coverage aid).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package resp

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestParseRandomStress(t *testing.T) {
	r := rand.New(rand.NewSource(12345))
	buf := make([]byte, 512)
	for i := 0; i < 80000; i++ {
		n := r.Intn(len(buf) + 1)
		for j := 0; j < n; j++ {
			buf[j] = byte(r.Intn(256))
		}
		p := NewParser(bytes.NewReader(buf[:n]))
		_, _ = p.Parse()
	}
}
