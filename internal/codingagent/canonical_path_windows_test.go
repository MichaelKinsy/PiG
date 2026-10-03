//go:build windows

package codingagent

import (
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

// A rooted link target such as \inner names a path on a drive, and filepath.EvalSymlinks resolves it on the process drive, where a second absolute link gives its result a volume. The process drive, a subst drive that is the current directory, holds decoys under the names the rooted targets use. The links live on a second subst drive. There Windows resolves a rooted target on the drive's backing volume, where it does not exist: on Windows 10 19045, Node's realpathSync and realpathSync.native both throw ENOENT for such a link (probed with Node 24.19.0), and Pi's canonicalizePath (paths.ts:28-33) then returns the path it was given. Every PiG canonicalizer must return that path too, not a decoy. An absolute link on the link drive is the control that Node resolves.
func TestCanonicalizePathResolvesRootedLinkOnLinkDriveNotProcessDrive(t *testing.T) {
	processDrive := testenv.SubstDrive(t, t.TempDir()) + `\`
	linkDrive := testenv.SubstDrive(t, t.TempDir()) + `\`
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
	paths := []string{linkDrive + "outer", linkDrive + "chain", linkDrive + "inner"}
	out, err := exec.CommandContext(t.Context(), "node", append([]string{"-e", `
const fs = require("node:fs");
const outcome = (realpath) => (p) => { try { return { path: realpath(p) }; } catch (error) { return { code: error.code }; } };
const paths = process.argv.slice(1);
console.log(JSON.stringify({ js: paths.map(outcome(fs.realpathSync)), native: paths.map(outcome(fs.realpathSync.native)) }));
`}, paths...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct{ Path, Code string }
	var node struct{ JS, Native []outcome }
	if err := json.Unmarshal(out, &node); err != nil {
		t.Fatal(err)
	}
	if len(node.JS) != len(paths) || len(node.Native) != len(paths) {
		t.Fatalf("oracle returned %s for %q", out, paths)
	}
	expected := []outcome{{Code: "ENOENT"}, {Code: "ENOENT"}, {Path: linkDrive + "right.txt"}}
	for i, path := range paths {
		if node.JS[i].Code != expected[i].Code || !strings.EqualFold(node.JS[i].Path, expected[i].Path) || node.Native[i].Code != expected[i].Code {
			t.Fatalf("Node realpathSync(%q)=%+v and realpathSync.native=%+v; fixture expects %+v", path, node.JS[i], node.Native[i], expected[i])
		}
		// Pi's canonicalizePath returns the path it was given when realpathSync throws.
		want := node.JS[i].Path
		if node.JS[i].Code != "" {
			want = path
		}
		for name, canonicalize := range map[string]func(string) string{
			"public": CanonicalizePath, "skills": canonicalizePath, "context": canonicalPath, "session": canonicalDir,
		} {
			if got := canonicalize(path); got != want {
				t.Errorf("%s(%q)=%q; Pi=%q", name, path, got, want)
			}
		}
	}
}
