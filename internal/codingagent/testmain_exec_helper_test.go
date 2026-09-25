package codingagent

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// execEchoHelperEnv, when set to "1", makes this test binary behave like a
// portable `echo`: print the remaining arguments space-joined with a
// trailing newline, then exit, without running any tests. A test that needs
// to exec a real, natively executable command (through pi.exec, which spawns
// the requested command directly with no shell) sets this in the child's
// environment and passes os.Executable() (this same test binary) as the
// command, so the same test works on every OS the test binary runs on,
// including native Windows, instead of depending on /bin/echo or a
// cmd.exe-only builtin. Mirrors cmd/pig/testmain_test.go's identical helper.
const execEchoHelperEnv = "PIG_TEST_EXEC_ECHO_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(execEchoHelperEnv) == "1" {
		fmt.Println(strings.Join(os.Args[1:], " "))
		os.Exit(0)
	}
	os.Exit(m.Run())
}
