// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// RESP2 decoded value types for Super-Cache.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package resp

// Value is a single RESP2 value: simple string, error, integer, bulk string, or array.
type Value struct {
	// Type is the RESP prefix byte: '+', '-', ':', '$', or '*'. Inline commands are represented as '*'.
	Type byte
	// Str holds simple strings, errors, and bulk string payloads.
	Str string
	// Integer holds RESP integer values when Type is ':'.
	Integer int64
	// Array holds nested values when Type is '*'.
	Array []Value
	// IsNull is true for null bulk strings ($-1) and null arrays (*-1).
	IsNull bool
}
