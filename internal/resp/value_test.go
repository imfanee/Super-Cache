// Tests for RESP Value types.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package resp

import "testing"

func TestValueZero(t *testing.T) {
	var v Value
	if v.Type != 0 || v.Str != "" || v.Integer != 0 || v.Array != nil || v.IsNull {
		t.Fatalf("zero value: %+v", v)
	}
}
