package env

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// ssh.ts:191,201 keep a line unless line.trim() is empty; trim removes U+FEFF and Unicode spaces but not U+0085 (strings.TrimSpace does the opposite).
func TestNonBlankLinesTrimsLikeJavaScript(t *testing.T) {
	got := nonBlankLines("\ufeff\nkeep\n\u00a0 \u3000\n\u0085\n")
	if want := []string{"keep", "\u0085"}; !slices.Equal(got, want) {
		t.Errorf("nonBlankLines = %q, want %q", got, want)
	}
}

// ssh.ts:260 stores each accepted line as line.trim(): a leading BOM goes, as String.prototype.trim removes U+FEFF.
func TestAcceptHostKeyStoresTheLineTrimmedLikeJavaScript(t *testing.T) {
	file := filepath.Join(t.TempDir(), "known_hosts")
	target := SshTarget{Host: "h", KnownHostsFile: file, HostKeyAlias: "pi-env-test"}
	mustDo(AcceptHostKey(target, []string{"\ufeffpi-env-test ssh-ed25519 AAAAC3NzaC1lZDI1NTE5\u3000"}))
	if content := string(must(os.ReadFile(file))); content != "pi-env-test ssh-ed25519 AAAAC3NzaC1lZDI1NTE5\n" {
		t.Fatalf("known-hosts file = %q", content)
	}
}
