package nodespawn

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// quietCommand is a command that would run the test binary to list no test,
// which writes nothing and exits 0, if Start started it.
func quietCommand(t *testing.T) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exec.Command(exe, "-test.list=^$")
}

// requireClosed requires that file was closed: closing it again reports
// os.ErrClosed.
func requireClosed(t *testing.T, name string, file *os.File) {
	t.Helper()
	if err := file.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("%s after Start: Close error %v, want os.ErrClosed", name, err)
	}
}

// Start takes only nil or an *os.File as each standard file, because it may
// start the program itself, where no goroutine copies a reader or a writer.
// It refuses any other before it starts anything.
func TestStartRejectsAStandardFileThatIsNotAnOSFile(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(*exec.Cmd)
	}{
		{"stdin reader", func(cmd *exec.Cmd) { cmd.Stdin = strings.NewReader("input") }},
		{"stdout writer", func(cmd *exec.Cmd) { cmd.Stdout = &bytes.Buffer{} }},
		{"stderr writer", func(cmd *exec.Cmd) { cmd.Stderr = io.Discard }},
	} {
		t.Run(c.name, func(t *testing.T) {
			cmd := quietCommand(t)
			c.set(cmd)
			if err := Start(cmd); err == nil || !strings.Contains(err.Error(), "only nil or an *os.File") {
				t.Fatalf("Start = %v, want the standard file refusal", err)
			}
			if cmd.Process != nil {
				t.Fatal("Start started a refused command")
			}
		})
	}
}

// Start watches no context, so it refuses a command whose Cancel or WaitDelay
// asks for that before it starts anything.
func TestStartRejectsACancelOrAWaitDelay(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		cmd  func() *exec.Cmd
	}{
		{"CommandContext's Cancel", func() *exec.Cmd { return exec.CommandContext(context.Background(), exe, "-test.list=^$") }},
		{"a WaitDelay", func() *exec.Cmd {
			cmd := exec.Command(exe, "-test.list=^$")
			cmd.WaitDelay = time.Second
			return cmd
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cmd := c.cmd()
			if err := Start(cmd); err == nil || !strings.Contains(err.Error(), "Cancel or a WaitDelay") {
				t.Fatalf("Start = %v, want the context refusal", err)
			}
			if cmd.Process != nil {
				t.Fatal("Start started a refused command")
			}
		})
	}
}

// Start closes the parent's copy of each file it gives the child, other than
// PiG's own standard files, whether the start succeeds, fails, or is refused.
// A pipe's reader then sees the end of the child's output when the child
// exits.
func TestStartClosesTheChildsFiles(t *testing.T) {
	t.Run("the child starts", func(t *testing.T) {
		stdinRead, stdinWrite, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stdinWrite.Close() }()
		outRead, outWrite, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = outRead.Close() }()
		cmd := quietCommand(t)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinRead, outWrite, outWrite
		if err := Start(cmd); err != nil {
			t.Fatal(err)
		}
		requireClosed(t, "stdin's read end", stdinRead)
		requireClosed(t, "stdout's write end", outWrite)
		if _, err := io.ReadAll(outRead); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("PiG's own standard files stay open", func(t *testing.T) {
		cmd := quietCommand(t)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := Start(cmd); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
		for name, file := range map[string]*os.File{"os.Stdin": os.Stdin, "os.Stdout": os.Stdout, "os.Stderr": os.Stderr} {
			if _, err := file.Stat(); err != nil {
				t.Errorf("%s after Start: %v", name, err)
			}
		}
	})
	t.Run("the start fails", func(t *testing.T) {
		_, outWrite, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd := quietCommand(t)
		cmd.Path += ".missing"
		cmd.Stdout = outWrite
		if err := Start(cmd); err == nil {
			t.Fatal("Start of a missing program succeeded")
		}
		requireClosed(t, "stdout's write end", outWrite)
	})
	t.Run("the command is refused", func(t *testing.T) {
		_, outWrite, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd := quietCommand(t)
		cmd.Stdout = outWrite
		cmd.WaitDelay = time.Second
		if err := Start(cmd); err == nil {
			t.Fatal("Start of a refused command succeeded")
		}
		requireClosed(t, "stdout's write end", outWrite)
	})
}
