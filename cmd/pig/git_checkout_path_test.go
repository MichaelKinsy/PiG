package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// A Git source checks out to <install root>/<host>/<repository path>.
// Windows cannot name a directory with ':', so there a git:file:// source's
// leading drive becomes a plain segment (C: to C), and any other segment
// containing ':' is refused with upstream resolveManagedPath's error, since
// NTFS reads name:stream as an alternate data stream (lead answer to Q10).
func TestGitCheckoutPathOnThisPlatform(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	root := codingagent.GitInstallRoot("", agentDir, false)
	windows := runtime.GOOS == "windows"

	drive := "C:"
	if windows {
		drive = "C"
	}
	got, err := packagemanager.GitCheckoutPath(t.TempDir(), agentDir, "git:file://localhost/C:/Users/me/owner/piglet.git", false)
	if want := filepath.Join(root, "localhost", drive, "Users", "me", "owner", "piglet"); err != nil || got != want {
		t.Fatalf("file URL checkout = %q, %v; want %q", got, err, want)
	}

	for _, source := range []string{"git:https://example.com/owner/repo:stream", "git:https://example.com/C:/owner/repo"} {
		got, err := packagemanager.GitCheckoutPath(t.TempDir(), agentDir, source, false)
		if !windows {
			if err != nil {
				t.Errorf("%s: %v; a ':' segment is an ordinary name here", source, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "Refusing to use path outside package install root") {
			t.Errorf("%s = %q, %v; want the install root refusal", source, got, err)
		}
	}
}

// Every platform's checkout rule. GitCheckoutRelative resolves with the host's
// Node path semantics, and Node's win32 resolver reads a "C:" segment as a
// drive and drops it, so the linux rule with a drive-shaped segment needs a
// non-Windows host. A ':' elsewhere in a segment, as in the darwin rule, is an
// ordinary name to that resolver and runs on every host.
func TestGitCheckoutRelativePerPlatform(t *testing.T) {
	for _, tc := range []struct {
		goos, source string
		want         []string
		refused      bool
		driveSegment bool
	}{
		{"windows", "git:file://localhost/C:/Users/me/owner/piglet.git", []string{"localhost", "C", "Users", "me", "owner", "piglet"}, false, false},
		{"windows", "git:file://localhost/d:/src/owner/piglet", []string{"localhost", "d", "src", "owner", "piglet"}, false, false},
		{"windows", "git:https://example.com/owner/repo", []string{"example.com", "owner", "repo"}, false, false},
		{"windows", "git:https://example.com/owner/repo:stream", nil, true, false},
		{"windows", "git:https://example.com/C:/owner/repo", nil, true, false},
		{"windows", "git:file://localhost/src/C:/owner/piglet", nil, true, false},
		{"linux", "git:file://localhost/C:/Users/me/owner/piglet.git", []string{"localhost", "C:", "Users", "me", "owner", "piglet"}, false, true},
		{"darwin", "git:https://example.com/owner/repo:stream", []string{"example.com", "owner", "repo:stream"}, false, false},
	} {
		if tc.driveSegment && runtime.GOOS == "windows" {
			continue
		}
		ref, err := sourceref.Parse(tc.source, sourceref.Options{Bare: sourceref.BareReject})
		if err != nil {
			t.Fatalf("parse %s: %v", tc.source, err)
		}
		got, err := packagemanager.GitCheckoutRelative(tc.goos, "root", ref)
		if tc.refused {
			if err == nil || !strings.Contains(err.Error(), "Refusing to use path outside package install root") {
				t.Errorf("%s %s = %q, %v; want the install root refusal", tc.goos, tc.source, got, err)
			}
			continue
		}
		if want := filepath.Join(tc.want...); err != nil || got != want {
			t.Errorf("%s %s = %q, %v; want %q", tc.goos, tc.source, got, err, want)
		}
	}
}

// Git for Windows reads file://localhost/C:/... as the UNC path
// //localhost/C:/..., so a local file URL reaches git there as file:///C:/...;
// other URLs, and every URL elsewhere, pass through unchanged.
func TestGitCloneRepoPerPlatform(t *testing.T) {
	for _, tc := range []struct{ goos, repo, want string }{
		{"windows", "file://localhost/C:/src/owner/piglet.git", "file:///C:/src/owner/piglet.git"},
		{"windows", "FILE://LOCALHOST/d:/src/owner/piglet", "file:///d:/src/owner/piglet"},
		{"windows", "file://server/share/owner/piglet.git", "file://server/share/owner/piglet.git"},
		{"windows", "https://example.com/owner/repo", "https://example.com/owner/repo"},
		{"linux", "file://localhost/tmp/owner/piglet.git", "file://localhost/tmp/owner/piglet.git"},
	} {
		if got := packagemanager.GitCloneRepo(tc.goos, tc.repo); got != tc.want {
			t.Errorf("gitCloneRepo(%s, %q) = %q, want %q", tc.goos, tc.repo, got, tc.want)
		}
	}
}
