package env

import (
	"os/exec"
	"strings"
	"testing"
)

// writeProgram writes an executable POSIX shell script through a child shell, so this process never holds the file open for
// writing. A descriptor open for writing is inherited by every child another goroutine forks until that child execs, and an
// execve of the file meanwhile fails with ETXTBSY (golang/go#22315); parallel tests in this package fork constantly.
func writeProgram(t *testing.T, path, script string) {
	t.Helper()
	command := exec.Command("/bin/sh", "-c", `cat > "$1" && chmod 700 "$1"`, "sh", path)
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("write %s: %v: %s", path, err, output)
	}
}
