// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Line-delimited JSON requests and responses for the management socket.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package mgmt

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// MgmtRequest is one management API request (NDJSON line).
type MgmtRequest struct {
	Secret string          `json:"secret"`
	Cmd    string          `json:"cmd"`
	Args   json.RawMessage `json:"args,omitempty"`
}

// MgmtResponse is the JSON body returned for one management request.
type MgmtResponse struct {
	OK    bool        `json:"ok"`
	Data  interface{} `json:"data,omitempty"`
	Error string      `json:"error,omitempty"`
}

func readLine(br *bufio.Reader) ([]byte, error) {
	line, err := br.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line, nil
}

func writeLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))
	return err
}
