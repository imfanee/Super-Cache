// Tests for logging setup.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package logging

import (
	"strings"
	"testing"

	"github.com/supercache/supercache/internal/config"
)

func TestInitStdout(t *testing.T) {
	c := &config.Config{SharedSecret: strings.Repeat("a", 32), LogOutput: "stdout", LogLevel: "debug"}
	config.ApplyDefaults(c)
	cleanup, err := Init(c)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
}
