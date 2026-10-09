//go:build !pig_strip_self_update

package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Pi 0.87.1 handlePackageCommand prints "Extensions are skipped. Run pi update --extensions to update extensions."
// before a bare `update`, and only then: an explicit self target or --self does not print it.
func TestBareUpdateReportsSkippedExtensionsLikePi(t *testing.T) {
	const note = "Extensions are skipped. Run pig update --extensions to update extensions.\n"
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"update"}, note},
		{[]string{"update", "--self"}, ""},
		{[]string{"update", "self"}, ""},
		{[]string{"update", "pig"}, ""},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			newPackageCommandPathsFixture(t)
			server := upToDateManifest(t)
			t.Cleanup(server.Close)
			t.Setenv("PIG_UPDATE_URL", server.URL)
			stdout, stderr, code := capturePackageCommand(t, tc.args...)
			assert.Equal(t, 0, code)
			assert.Equal(t, tc.want, stdout)
			assert.Equal(t, "pig "+selfUpdateVersion()+" is up to date.\n", stderr)
		})
	}
}

func TestRunPackageCommand_SelfUpdateTargetWithoutSourceShowsFallback(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_UPDATE_URL", "")
	for _, tc := range []struct {
		args       []string
		wantStdout string
	}{
		{[]string{"update", "self"}, ""},
		// Pi prints the skipped-extensions note before a bare self-update.
		{[]string{"update"}, "Extensions are skipped. Run pig update --extensions to update extensions.\n"},
	} {
		args := tc.args
		stdout, stderr, code := captureStdoutStderr(t, func() int {
			return runPackageCommand(args)
		})
		if code != 1 {
			t.Fatalf("%v: code = %d, want 1", args, code)
		}
		if stdout != tc.wantStdout {
			t.Fatalf("%v: stdout = %q, want %q", args, stdout, tc.wantStdout)
		}
		// Writability alone no longer proves standalone ownership. Without the
		// installer receipt, the resolver refuses mutation and names the path.
		if !strings.Contains(stderr, "cannot self-update this installation") || !strings.Contains(stderr, "Executable:") {
			t.Fatalf("%v: stderr missing unknown-provenance remediation:\n%s", args, stderr)
		}
		if strings.Contains(stderr, "legacy-update-host") || strings.Contains(stderr, "pig update") {
			t.Fatalf("%v: stderr contains stale or looping remediation:\n%s", args, stderr)
		}
	}
}
