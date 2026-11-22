// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	fakeGitModeEnv   = "BUGLEDGER_FAKE_GIT_MODE"
	fakeGitStderrEnv = "BUGLEDGER_FAKE_GIT_STDERR"
)

// The test binary doubles as a fake git for timeout and failure tests, so no shell or child sleep process is spawned and the test runs on every platform.
func init() {
	switch os.Getenv(fakeGitModeEnv) {
	case "sleep":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "fast-stream":
		line := strings.Repeat("x", 1024) + "\n"
		for range 512 {
			fmt.Print(line)
		}
		os.Exit(0)
	case "fail":
		fmt.Fprint(os.Stderr, os.Getenv(fakeGitStderrEnv))
		os.Exit(1)
	}
}

func installFakeGit(t *testing.T, mode, stderr string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name = "git.exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv(fakeGitModeEnv, mode)
	t.Setenv(fakeGitStderrEnv, stderr)
}

func TestGitTimeoutSaysWhy(t *testing.T) {
	const bound = 200 * time.Millisecond

	t.Run("git timeout", func(t *testing.T) {
		installFakeGit(t, "sleep", "")
		r := repo{root: t.TempDir()}
		_, err := r.gitWithTimeout(bound, "log", "--reverse", "-U100000", "HEAD")
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		msg := err.Error()
		want := fmt.Sprintf("git log did not finish in %s: the host is slow or loaded, or the reader of its output is slow", bound)
		if !strings.Contains(msg, want) {
			t.Fatalf("err = %q, want substring %q", msg, want)
		}
		if strings.Contains(msg, "signal: killed") {
			t.Fatalf("err = %q, must not report bare signal: killed", msg)
		}
	})

	t.Run("gitLines timeout on slow git", func(t *testing.T) {
		installFakeGit(t, "sleep", "")
		r := repo{root: t.TempDir()}
		err := r.gitLinesWithTimeout(bound, []string{"rev-list", "HEAD"}, func(string) {})
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		msg := err.Error()
		want := fmt.Sprintf("git rev-list did not finish in %s: the host is slow or loaded, or the reader of its output is slow", bound)
		if !strings.Contains(msg, want) {
			t.Fatalf("err = %q, want substring %q", msg, want)
		}
		if strings.Contains(msg, "signal: killed") {
			t.Fatalf("err = %q, must not report bare signal: killed", msg)
		}
	})

	t.Run("gitLines timeout on slow reader", func(t *testing.T) {
		installFakeGit(t, "fast-stream", "")
		r := repo{root: t.TempDir()}
		slept := false
		err := r.gitLinesWithTimeout(bound, []string{"log", "-p"}, func(string) {
			if !slept {
				slept = true
				time.Sleep(bound * 2)
			}
		})
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		msg := err.Error()
		want := fmt.Sprintf("git log did not finish in %s: the host is slow or loaded, or the reader of its output is slow", bound)
		if !strings.Contains(msg, want) {
			t.Fatalf("err = %q, want substring %q", msg, want)
		}
		if strings.Contains(msg, "signal: killed") {
			t.Fatalf("err = %q, must not report bare signal: killed", msg)
		}
	})

	t.Run("git exit 1 keeps stderr", func(t *testing.T) {
		installFakeGit(t, "fail", "fatal: bad revision")
		r := repo{root: t.TempDir()}
		_, err := r.gitWithTimeout(bound, "show", "no-such-rev")
		if err == nil {
			t.Fatal("expected git error, got nil")
		}
		msg := err.Error()
		if !strings.Contains(msg, "fatal: bad revision") {
			t.Fatalf("err = %q, want stderr preserved", msg)
		}
		if strings.Contains(msg, "the host is slow or loaded") {
			t.Fatalf("err = %q, real git failure must not blame slow host", msg)
		}
	})

	t.Run("gitLines exit 1 keeps stderr", func(t *testing.T) {
		installFakeGit(t, "fail", "fatal: bad revision")
		r := repo{root: t.TempDir()}
		err := r.gitLinesWithTimeout(bound, []string{"show", "no-such-rev"}, func(string) {})
		if err == nil {
			t.Fatal("expected git error, got nil")
		}
		msg := err.Error()
		if !strings.Contains(msg, "fatal: bad revision") {
			t.Fatalf("err = %q, want stderr preserved", msg)
		}
		if strings.Contains(msg, "the host is slow or loaded") {
			t.Fatalf("err = %q, real git failure must not blame slow host", msg)
		}
	})
}
