//go:build !windows

package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

// The identity of a link is its own, never the target's (Pi's `lstatSync`): a link to a directory moved away does not follow it.
func TestIdentityOfALinkIsNotTheIdentityOfItsTarget(t *testing.T) {
	top := realOf(t, tempRoot(t))
	dir := filepath.Join(top, "d")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(top, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if got := identityOf(link); !got.present || got == identityOf(dir) {
		t.Errorf("identity of a link = %+v, the target's is %+v: a link is not followed", got, identityOf(dir))
	}
}
