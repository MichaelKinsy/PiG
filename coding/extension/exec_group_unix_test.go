//go:build unix

package extension_test

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func init() {
	// pgid: report whether this process shares its parent's process group.
	treeHelpers["pgid"] = func([]string) {
		group, _ := syscall.Getpgid(0)
		parent, _ := syscall.Getpgid(os.Getppid())
		fmt.Println(group == parent)
	}
}

// Pi spawns the child without `detached`, so it stays in the caller's process
// group; nothing signals a group.
func TestExecCommandChildStaysInTheCallersProcessGroup(t *testing.T) {
	command, args := helper(t, "pgid")
	result, err := extension.ExecCommand(context.Background(), t.TempDir(), command, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "true\n" {
		t.Fatalf("child in the caller's process group = %q, want true", result.Stdout)
	}
}
