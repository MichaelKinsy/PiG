//go:build windows

package codingagent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi paths.ts:28-33 uses Node realpathSync, not realpathSync.native. libuv fs.c:284-303 rejects volume-GUID mount targets as symbolic links; Node therefore retains the mount's directory name instead of replacing it with a volume GUID.
func TestCanonicalizePathPreservesVolumeMountPoints(t *testing.T) {
	root := t.TempDir()
	drive := filepath.VolumeName(root) + `\`
	driveName, err := windows.UTF16PtrFromString(drive)
	if err != nil {
		t.Fatal(err)
	}
	var volume [windows.MAX_PATH + 1]uint16
	if err := windows.GetVolumeNameForVolumeMountPoint(driveName, &volume[0], uint32(len(volume))); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "marker")
	if err := os.WriteFile(marker, []byte("mounted"), 0o600); err != nil {
		t.Fatal(err)
	}
	mount := filepath.Join(root, "mount")
	testenv.RequireDirectoryLink(t, windows.UTF16ToString(volume[:]), mount)
	t.Cleanup(func() {
		if err := os.Remove(mount); err != nil {
			t.Error(err)
		}
	})
	rel, err := filepath.Rel(drive, target)
	if err != nil {
		t.Fatal(err)
	}
	mountedMarker := filepath.Join(mount, rel, "marker")
	if data, err := os.ReadFile(mountedMarker); err != nil || string(data) != "mounted" {
		t.Fatalf("volume fixture content=%q error=%v", data, err)
	}
	alias := filepath.Join(root, "alias")
	testenv.RequireDirectoryLink(t, mount, alias)
	shortcut := filepath.Join(target, "shortcut")
	testenv.RequireDirectoryLink(t, root, shortcut)
	paths := []string{
		mount,
		mountedMarker,
		filepath.Join(alias, rel, "marker"),
		filepath.Join(mount, rel, "shortcut", "target", "marker"),
		filepath.Join(mount, rel, "missing"),
	}
	// Keep a native Node oracle in the regression rather than assuming that all reparse records have the same realpath semantics.
	out, err := exec.CommandContext(t.Context(), "node", append([]string{"-e", `const fs=require('node:fs'); console.log(JSON.stringify(process.argv.slice(1).map(p=>{try{return fs.realpathSync(p)}catch{return p}})))`}, paths...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(paths) {
		t.Fatalf("oracle returned %q for %q", want, paths)
	}
	for i, path := range paths {
		for name, canonicalize := range map[string]func(string) string{
			"public": CanonicalizePath, "skills": canonicalizePath, "context": canonicalPath, "session": canonicalDir,
		} {
			if got := canonicalize(path); got != want[i] {
				t.Errorf("%s(%q)=%q; Node=%q", name, path, got, want[i])
			}
		}
	}
	// Ordinary alias paths merge, but a mount path and its drive path stay distinct in Pi's resource identity.
	got := DedupBySymlink([]string{mountedMarker, filepath.Join(alias, rel, "marker"), marker})
	if len(got) != 2 || got[0].Canonical != want[1] || got[1].Canonical != CanonicalizePath(marker) {
		t.Fatalf("resource identities=%+v; want mount and drive paths, with the alias merged", got)
	}
}

func TestVolumeGUIDPathClassification(t *testing.T) {
	for path, want := range map[string]bool{
		`\\?\Volume{1234}\`:           true,
		`\\?\volume{1234}\child`:      true,
		`C:\Volume{1234}`:             false,
		`\\?\C:\ordinary`:             false,
		`\\server\share\Volume{1234}`: false,
	} {
		if got := isVolumeGUIDPath(path); got != want {
			t.Errorf("isVolumeGUIDPath(%q)=%v; want %v", path, got, want)
		}
	}
}

// Node's realpathSync resolves each link target with path.resolve(parent, target), so a rooted relative symbolic link such as \dir names a directory on the drive of the path that reached it. The volume-mount walk must not join it under the link's parent, and filepath.EvalSymlinks must not drop that drive, with or without a mount before the link.
func TestCanonicalizePathResolvesRootedLinkTargetsBeyondVolumeMounts(t *testing.T) {
	root := t.TempDir()
	drive := filepath.VolumeName(root) + `\`
	driveName, err := windows.UTF16PtrFromString(drive)
	if err != nil {
		t.Fatal(err)
	}
	var volume [windows.MAX_PATH + 1]uint16
	if err := windows.GetVolumeNameForVolumeMountPoint(driveName, &volume[0], uint32(len(volume))); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "marker"), []byte("rooted"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootedTarget := strings.TrimPrefix(target, filepath.VolumeName(target))
	rooted := filepath.Join(root, "rooted")
	// A rooted relative symbolic link needs symlink privilege; a junction always stores an absolute target. The privilege is required: a host without it fails instead of skipping the only regression for a rooted target.
	testenv.RequireSymlink(t, rootedTarget, rooted)
	// An absolute link to the rooted link resolves the rooted target again from the link's own path.
	chain := filepath.Join(root, "chain")
	testenv.RequireSymlink(t, rooted, chain)
	mount := filepath.Join(root, "mount")
	testenv.RequireDirectoryLink(t, windows.UTF16ToString(volume[:]), mount)
	t.Cleanup(func() {
		if err := os.Remove(mount); err != nil {
			t.Error(err)
		}
	})
	rel, err := filepath.Rel(drive, rooted)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(rooted, "marker"), filepath.Join(chain, "marker"), filepath.Join(mount, rel, "marker")}
	for _, path := range paths {
		if data, err := os.ReadFile(path); err != nil || string(data) != "rooted" {
			t.Fatalf("rooted link fixture %q content=%q error=%v", path, data, err)
		}
	}
	out, err := exec.CommandContext(t.Context(), "node", append([]string{"-e", `const fs=require('node:fs'); console.log(JSON.stringify(process.argv.slice(1).map(p=>{try{return fs.realpathSync(p)}catch{return p}})))`}, paths...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(paths) {
		t.Fatalf("oracle returned %q for %q", want, paths)
	}
	for i, path := range paths {
		// Node's realpathSync throws for a failure and the oracle then echoes the input, which every fallback would also return. Require an actual resolution.
		if strings.EqualFold(want[i], path) {
			t.Fatalf("Node did not resolve %q; the oracle returned its input", path)
		}
		if got := CanonicalizePath(path); got != want[i] {
			t.Errorf("CanonicalizePath(%q)=%q; Node=%q", path, got, want[i])
		}
	}
}

// substDrive maps a free drive letter to dir for the test and returns the drive with a trailing separator.
func substDrive(t *testing.T, dir string) string {
	t.Helper()
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		t.Fatal(err)
	}
	for letter := 'Z'; letter >= 'D'; letter-- {
		if mask&(1<<(letter-'A')) != 0 {
			continue
		}
		drive := string(letter) + ":"
		if out, err := exec.CommandContext(t.Context(), "subst", drive, dir).CombinedOutput(); err != nil {
			t.Fatalf("subst %s %s: %v\n%s", drive, dir, err, out)
		}
		t.Cleanup(func() {
			if out, err := exec.CommandContext(context.WithoutCancel(t.Context()), "subst", drive, "/d").CombinedOutput(); err != nil {
				t.Errorf("subst %s /d: %v\n%s", drive, err, out)
			}
		})
		return drive + `\`
	}
	t.Fatal("no free drive letter for subst")
	return ""
}

// Node resolves a rooted link target on the device of the path that reached the link. filepath.EvalSymlinks resolves it on the process drive instead, and a second absolute link there gives its result a volume, so the volume alone cannot show that the target was resolved on the right drive. The process drive, a second subst drive that is the current directory, holds decoys under the names the rooted targets use.
func TestCanonicalizePathResolvesRootedLinkOnLinkDriveNotProcessDrive(t *testing.T) {
	processDrive := substDrive(t, t.TempDir())
	linkDrive := substDrive(t, t.TempDir())
	t.Chdir(processDrive)
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(processDrive + "wrong.txt")
	write(linkDrive + "right.txt")
	testenv.RequireSymlink(t, processDrive+"wrong.txt", processDrive+"inner")
	testenv.RequireSymlink(t, linkDrive+"right.txt", linkDrive+"inner")
	testenv.RequireSymlink(t, `\inner`, linkDrive+"outer")
	// A rooted link whose decoy on the process drive is another rooted link.
	write(processDrive + "wrong2.txt")
	write(linkDrive + "final.txt")
	testenv.RequireSymlink(t, `\wrong2.txt`, processDrive+"inner2")
	testenv.RequireSymlink(t, linkDrive+"final.txt", linkDrive+"right2")
	testenv.RequireSymlink(t, `\right2`, linkDrive+"inner2")
	testenv.RequireSymlink(t, `\inner2`, linkDrive+"chain")
	paths := []string{linkDrive + "outer", linkDrive + "chain"}
	out, err := exec.CommandContext(t.Context(), "node", append([]string{"-e", `const fs=require('node:fs'); console.log(JSON.stringify(process.argv.slice(1).map(p=>{try{return fs.realpathSync(p)}catch{return p}})))`}, paths...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(paths) {
		t.Fatalf("oracle returned %q for %q", want, paths)
	}
	expected := []string{linkDrive + "right.txt", linkDrive + "final.txt"}
	for i, path := range paths {
		if !strings.EqualFold(want[i], expected[i]) {
			t.Fatalf("Node realpathSync(%q)=%q; fixture expects %q", path, want[i], expected[i])
		}
		for name, canonicalize := range map[string]func(string) string{
			"public": CanonicalizePath, "skills": canonicalizePath, "context": canonicalPath, "session": canonicalDir,
		} {
			if got := canonicalize(path); got != want[i] {
				t.Errorf("%s(%q)=%q; Node=%q", name, path, got, want[i])
			}
		}
	}
}
