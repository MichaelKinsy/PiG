package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// Pi package-manager.ts:1532-1565,1630-1656 resolves only origin branches and never prompts during remote checks.
func TestGitAvailableUpdateRemoteResolution(t *testing.T) {
	for _, tc := range []struct {
		name, upstream, branchMode string
		offline, want              bool
		remote                     []string
	}{
		{"changed branch", "origin/feature/topic", "changed", false, true, []string{"refs/heads/feature/topic"}},
		{"unchanged branch ignores default HEAD", "origin/feature/topic", "same", false, false, []string{"refs/heads/feature/topic"}},
		{"foreign upstream uses HEAD", "other/feature", "changed", false, true, []string{"HEAD"}},
		{"empty origin branch uses HEAD", "origin/", "changed", false, true, []string{"HEAD"}},
		{"missing upstream uses HEAD", "", "changed", false, true, []string{"HEAD"}},
		{"empty branch response falls back", "origin/feature/topic", "empty", false, true, []string{"refs/heads/feature/topic", "HEAD"}},
		{"failed branch query does not fall back", "origin/feature/topic", "error", false, false, []string{"refs/heads/feature/topic"}},
		{"offline performs no commands", "origin/feature/topic", "changed", true, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, bin := t.TempDir(), t.TempDir()
			log := filepath.Join(cwd, "commands")
			t.Setenv("GIT_UPDATE_LOG", log)
			t.Setenv("GIT_UPDATE_UPSTREAM", tc.upstream)
			t.Setenv("GIT_UPDATE_BRANCH_MODE", tc.branchMode)
			t.Setenv("GIT_TERMINAL_PROMPT", "1")
			t.Setenv("PI_OFFLINE", "")
			t.Setenv("PIG_OFFLINE", "")
			if tc.offline {
				t.Setenv("PI_OFFLINE", "1")
			}
			writeStubScript(t, filepath.Join(bin, "git"), `#!/bin/sh
printf '%s|%s\n' "$GIT_TERMINAL_PROMPT" "$*" >> "$GIT_UPDATE_LOG"
case "$*" in
 'rev-parse HEAD') printf '%040d\n' 1 ;;
 'rev-parse --abbrev-ref @{upstream}')
  if [ -z "$GIT_UPDATE_UPSTREAM" ]; then exit 1; fi
  printf '%s\n' "$GIT_UPDATE_UPSTREAM" ;;
 'ls-remote origin refs/heads/feature/topic')
  case "$GIT_UPDATE_BRANCH_MODE" in
   error) exit 1 ;;
   empty) exit 0 ;;
   same) printf '%040d\trefs/heads/feature/topic\n' 1 ;;
   *) printf '%040d\trefs/heads/feature/topic\n' 2 ;;
  esac ;;
 'ls-remote origin HEAD') printf '%040d\tHEAD\n' 2 ;;
 *) exit 1 ;;
esac
`)
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if got := packagemanager.GitHasAvailableUpdate(cwd); got != tc.want {
				t.Errorf("update=%t, want %t", got, tc.want)
			}
			if tc.offline {
				requireNoUpdateQuery(t, log)
				return
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := []string{"1|rev-parse HEAD", "1|rev-parse --abbrev-ref @{upstream}"}
			for _, ref := range tc.remote {
				wantCalls = append(wantCalls, fmt.Sprintf("0|ls-remote origin %s", ref))
			}
			if got := strings.Split(strings.TrimSpace(string(data)), "\n"); !reflect.DeepEqual(got, wantCalls) {
				t.Errorf("calls=%q, want %q", got, wantCalls)
			}
			if os.Getenv("GIT_TERMINAL_PROMPT") != "1" {
				t.Fatal("remote query mutated parent environment")
			}
		})
	}
}
