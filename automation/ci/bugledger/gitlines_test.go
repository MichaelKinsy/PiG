// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	fakeGitEnv      = "BUGLEDGER_FAKE_GIT_SENTINEL"
	fakeGitWaitTime = 20 * time.Second
)

// The test binary doubles as a fake git (see TestGitLinesPassesLinesBeforeGitExits), so the test needs no shell and runs on every platform.
func init() {
	sentinel := os.Getenv(fakeGitEnv)
	if sentinel == "" {
		return
	}
	fmt.Print("first\n")
	deadline := time.Now().Add(fakeGitWaitTime)
	for {
		if _, err := os.Stat(sentinel); err == nil {
			break
		}
		if time.Now().After(deadline) {
			fmt.Fprint(os.Stderr, "the reader did not see the first line while git was still running")
			os.Exit(3)
		}
		time.Sleep(5 * time.Millisecond)
	}
	fmt.Print("second\n\nthird, with no newline")
	os.Exit(0)
}

// gitLines hands the reader each line while git is still printing. A reader that gets nothing until git exits holds the whole output, which for the diff of a commit that adds large generated files is a gigabyte.
func TestGitLinesPassesLinesBeforeGitExits(t *testing.T) {
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
	sentinel := filepath.Join(t.TempDir(), "seen-first-line")
	t.Setenv("PATH", bin)
	t.Setenv(fakeGitEnv, sentinel)
	var got []string
	err = repo{root: t.TempDir()}.gitLines([]string{"anything"}, func(l string) {
		got = append(got, l)
		if l == "first" {
			if err := os.WriteFile(sentinel, nil, 0o600); err != nil {
				t.Error(err)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"first", "second", "", "third, with no newline"}; !slices.Equal(got, want) {
		t.Fatalf("lines = %q, want %q", got, want)
	}
}

// gitLines splits what git prints as strings.SplitSeq(git(...), "\n") does, without the empty last element a final newline leaves.
func TestGitLinesMatchesTheBufferedSplit(t *testing.T) {
	r := testRepo{t: t, root: t.TempDir()}
	r.run("init", "-q", "-b", "main")
	contents := map[string]string{
		"newline-end.txt": "a\n\nb\n",
		"no-newline.txt":  "x\ny",
		"empty.txt":       "",
		"blank-lines.txt": "\n\n",
		"long-line.txt":   strings.Repeat("0123456789", 20000) + "\nshort\n",
	}
	var paths []string
	for p, c := range contents {
		r.write(p, c)
		paths = append(paths, p)
	}
	r.commit("add files", paths...)
	for _, p := range paths {
		var got []string
		if err := (repo{root: r.root}).gitLines([]string{"show", "HEAD:" + p}, func(l string) { got = append(got, l) }); err != nil {
			t.Fatal(err)
		}
		var want []string
		if c := contents[p]; c != "" {
			want = strings.Split(strings.TrimSuffix(c, "\n"), "\n")
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: %d lines, want %d (first got %q)", p, len(got), len(want), clip(strings.Join(got, "|"), 40))
		}
	}
}

// A failing git is an error with git's message, as repo.git reports it.
func TestGitLinesReportsAGitFailure(t *testing.T) {
	r := testRepo{t: t, root: t.TempDir()}
	r.run("init", "-q")
	called := false
	err := (repo{root: r.root}).gitLines([]string{"show", "no-such-revision"}, func(string) { called = true })
	if err == nil || !strings.Contains(err.Error(), "git show no-such-revision") || !strings.Contains(err.Error(), "no-such-revision") {
		t.Fatalf("err = %v, want git's failure for the missing revision", err)
	}
	if called {
		t.Fatal("a failing git passed output to the reader")
	}
}
