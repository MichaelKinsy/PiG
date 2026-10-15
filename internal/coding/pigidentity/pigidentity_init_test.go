package pigidentity

import (
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The identity values are constants, so the package has no initializer for a process to run. At a5df22bd3a its init decoded identity.json with encoding/json and checked it through a map: 15,928 bytes in 105 allocations in every pig process.
func TestPackageInitDoesNoWork(t *testing.T) {
	// The test binary runs no test (-test.run matches none) and reports every package initializer it runs.
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	command.Env = append(os.Environ(), "GODEBUG=inittrace=1")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("%s: %v\n%s", os.Args[0], err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "\ninit ") && !strings.HasPrefix(stderr.String(), "init ") {
		t.Fatalf("GODEBUG=inittrace=1 reported no initializer:\n%s", stderr.String())
	}
	if line := regexp.MustCompile(`(?m)^init github\.com/MichaelKinsy/PiG/internal/coding/pigidentity .*$`).FindString(stderr.String()); line != "" {
		t.Fatalf("pigidentity runs an initializer at process start: %s", line)
	}
}
