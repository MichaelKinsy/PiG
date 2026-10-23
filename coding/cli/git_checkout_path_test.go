package cli

import (
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// A Git source checks out to <install root>/<host>/<repository path>; a
// git:file:// source checks out to <host>/<basename>-<12 hex of the path's
// sha256> so the path stays short (Git for Windows' $GIT_DIR limit). On Windows
// a segment containing ':' is refused with upstream resolveManagedPath's error, since
// NTFS reads name:stream as an alternate data stream (lead answer to Q10).
func TestGitCheckoutPathOnThisPlatform(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	root := codingagent.GitInstallRoot("", agentDir, false)
	windows := runtime.GOOS == "windows"

	got, err := packagemanager.GitCheckoutPath(t.TempDir(), agentDir, "git:file://localhost/C:/Users/me/owner/piglet.git", false)
	if want := filepath.Join(root, "localhost", "a91ac64502db", "piglet"); err != nil || got != want {
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
	}{
		{"windows", "git:file://localhost/C:/Users/me/owner/piglet.git", []string{"localhost", "a91ac64502db", "piglet"}, false},
		{"windows", "git:file://localhost/d:/src/owner/piglet", []string{"localhost", "31ec0c491be0", "piglet"}, false},
		{"windows", "git:https://example.com/owner/repo", []string{"example.com", "owner", "repo"}, false},
		{"windows", "git:https://example.com/owner/repo:stream", nil, true},
		{"windows", "git:https://example.com/C:/owner/repo", nil, true},
		{"windows", "git:file://localhost/src/owner/piglet:stream", nil, true},
		{"linux", "git:file://localhost/C:/Users/me/owner/piglet.git", []string{"localhost", "a91ac64502db", "piglet"}, false},
		{"linux", "git:file://localhost/tmp/x/piglet", []string{"localhost", "fdb4f8773215", "piglet"}, false},
		{"darwin", "git:https://example.com/owner/repo:stream", []string{"example.com", "owner", "repo:stream"}, false},
	} {
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

// A local file:// source's checkout path embeds the whole source path and can
// pass Windows' MAX_PATH, so a Windows git child gets core.longpaths through the
// environment, which leaves Pi's git argv untouched. It joins any config entries
// the environment already has, and elsewhere the environment is unchanged.
func TestGitLongPathsEnvPerPlatform(t *testing.T) {
	for _, tc := range []struct {
		name, goos string
		env, want  []string
	}{
		{"windows empty", "windows", []string{"PATH=C:\\bin"}, []string{"PATH=C:\\bin", "GIT_CONFIG_KEY_0=core.longpaths", "GIT_CONFIG_VALUE_0=true", "GIT_CONFIG_COUNT=1"}},
		{"windows existing entries", "windows", []string{"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=a.b", "GIT_CONFIG_VALUE_0=c", "GIT_CONFIG_KEY_1=d.e", "GIT_CONFIG_VALUE_1=f"},
			[]string{"GIT_CONFIG_KEY_0=a.b", "GIT_CONFIG_VALUE_0=c", "GIT_CONFIG_KEY_1=d.e", "GIT_CONFIG_VALUE_1=f", "GIT_CONFIG_KEY_2=core.longpaths", "GIT_CONFIG_VALUE_2=true", "GIT_CONFIG_COUNT=3"}},
		{"windows mixed-case count", "windows", []string{"Git_Config_Count=1", "GIT_CONFIG_KEY_0=a.b", "GIT_CONFIG_VALUE_0=c"},
			[]string{"GIT_CONFIG_KEY_0=a.b", "GIT_CONFIG_VALUE_0=c", "GIT_CONFIG_KEY_1=core.longpaths", "GIT_CONFIG_VALUE_1=true", "GIT_CONFIG_COUNT=2"}},
		{"linux", "linux", []string{"GIT_CONFIG_COUNT=1"}, []string{"GIT_CONFIG_COUNT=1"}},
		{"darwin", "darwin", []string{"PATH=/bin"}, []string{"PATH=/bin"}},
	} {
		in := append([]string(nil), tc.env...)
		if got := packagemanager.GitLongPathsEnv(tc.goos, in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: GitLongPathsEnv = %q, want %q", tc.name, got, tc.want)
		}
		if !reflect.DeepEqual(in, tc.env) {
			t.Errorf("%s: input environment modified: %q", tc.name, in)
		}
	}
}
