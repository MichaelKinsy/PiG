//go:build unix

package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// sameFolderAsLockParent requires the parent of a lock folder to be the given folder, by identity (through any link).
func sameFolderAsLockParent(t *testing.T, lockPath, folder string) {
	t.Helper()
	lockParent, err := os.Stat(filepath.Dir(lockPath))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(folder)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(lockParent, want) {
		t.Fatalf("lock %s is not in folder %s", lockPath, folder)
	}
}

// R17: the store's folder is a link and the file is no link. The lock path keeps the folder link spelling, as Pi's does (lockfile.lock(authPath, { realpath: false })).
func TestR17FolderLinkNotResolved(t *testing.T) {
	root := t.TempDir() // not resolved: on macOS /var is a link to /private/var, and the spelling must stay as given
	realFolder := filepath.Join(root, "agent")
	if err := os.MkdirAll(realFolder, 0o700); err != nil {
		t.Fatal(err)
	}
	folderLink := filepath.Join(root, "agentlink")
	if err := os.Symlink(realFolder, folderLink); err != nil {
		t.Fatal(err)
	}
	via := filepath.Join(folderLink, "auth.json")
	if err := os.WriteFile(filepath.Join(realFolder, "auth.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveAuthPath(via)
	if err != nil || got != via {
		t.Fatalf("resolveAuthPath(%q) = %q, %v; want the path as given", via, got, err)
	}
	s, err := NewAuthStorage(via)
	if err != nil {
		t.Fatal(err)
	}
	err = s.withFileLock(t.Context(), false, func(*pilock.Lock) error {
		if fi, err := os.Stat(via + ".lock"); err != nil || !fi.IsDir() {
			t.Errorf("no lock folder at %s.lock: %v", via, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	setKey(t, s, "p", "fixture-refresh-A")
	if fi, err := os.Lstat(via); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("the store file must stay a plain file, got %v, %v", fi, err)
	}
}

// R18: the file is a link whose target sits behind a folder link. The lock is made in the target's real folder, and its spelling keeps the target as written.
func TestR18FileLinkThroughFolderLink(t *testing.T) {
	root := t.TempDir()
	realFolder := filepath.Join(root, "owner")
	if err := os.MkdirAll(realFolder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realFolder, "auth.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	folderLink := filepath.Join(root, "ownerlink")
	if err := os.Symlink(realFolder, folderLink); err != nil {
		t.Fatal(err)
	}
	workerDir := filepath.Join(root, "worker")
	if err := os.MkdirAll(workerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workerDir, "auth.json")
	if err := os.Symlink(filepath.Join(folderLink, "auth.json"), link); err != nil {
		t.Fatal(err)
	}
	written := filepath.Join(folderLink, "auth.json")
	got, err := resolveAuthPath(link)
	if err != nil || got != written {
		t.Fatalf("resolveAuthPath(%q) = %q, %v; want the target as written, %q", link, got, err, written)
	}
	s, err := NewAuthStorage(link)
	if err != nil {
		t.Fatal(err)
	}
	err = s.withFileLock(t.Context(), false, func(*pilock.Lock) error {
		if fi, err := os.Stat(got + ".lock"); err != nil || !fi.IsDir() {
			t.Errorf("no lock folder at %s.lock: %v", got, err)
		}
		sameFolderAsLockParent(t, got+".lock", realFolder)
		if _, err := os.Lstat(link + ".lock"); err == nil {
			t.Errorf("lock taken at the link path %s", link)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	setKey(t, s, "p", "fixture-refresh-A")
	if li, err := os.Lstat(link); err != nil || li.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link is no longer a link: %v, %v", li, err)
	}
}

// R19 (a): two processes, two spellings of one store, one of them through a folder link. Each writes its marks under the store's lock; the log must show every enter followed at once by its own leave, so the two never sit inside together.
func TestR19TwoProcessesTwoSpellingsOneLock(t *testing.T) {
	root := t.TempDir()
	realFolder := filepath.Join(root, "owner")
	if err := os.MkdirAll(realFolder, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(realFolder, "auth.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	folderLink := filepath.Join(root, "ownerlink")
	if err := os.Symlink(realFolder, folderLink); err != nil {
		t.Fatal(err)
	}
	via := filepath.Join(folderLink, "auth.json")
	logPath := filepath.Join(root, "marks.log")
	const rounds = 5
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestR19ContenderHelper$")
	cmd.Env = append(os.Environ(), "PIG_TEST_R19_PATH="+via, "PIG_TEST_R19_LOG="+logPath, "PIG_TEST_R19_ID=child", "PIG_TEST_R19_ROUNDS="+strconv.Itoa(rounds))
	var childOut strings.Builder
	cmd.Stdout, cmd.Stderr = &childOut, &childOut
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The parent runs the same loop through the plain spelling while the child runs through the folder link.
	parentErr := contendLoop(target, logPath, "parent", rounds)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child: %v\n%s", err, childOut.String())
	}
	if parentErr != nil {
		t.Fatal(parentErr)
	}
	checkAlternating(t, logPath, 2*rounds)
}

// TestR19ContenderHelper is the child process of TestR19TwoProcessesTwoSpellingsOneLock. It runs only when its environment names a store.
func TestR19ContenderHelper(t *testing.T) {
	path := os.Getenv("PIG_TEST_R19_PATH")
	if path == "" {
		return
	}
	rounds, _ := strconv.Atoi(os.Getenv("PIG_TEST_R19_ROUNDS"))
	if err := contendLoop(path, os.Getenv("PIG_TEST_R19_LOG"), os.Getenv("PIG_TEST_R19_ID"), rounds); err != nil {
		t.Fatal(err)
	}
}

// contendLoop takes the store's lock (through Modify) rounds times; inside, it appends an enter mark, waits, and appends the leave mark.
func contendLoop(path, logPath, id string, rounds int) error {
	s, err := NewAuthStorage(path)
	if err != nil {
		return err
	}
	for i := range rounds {
		_, err := s.Modify(context.Background(), "p"+id, func(*Credential) (*Credential, error) {
			if err := appendMark(logPath, "enter "+id+" "+strconv.Itoa(i)); err != nil {
				return nil, err
			}
			time.Sleep(30 * time.Millisecond)
			if err := appendMark(logPath, "leave "+id+" "+strconv.Itoa(i)); err != nil {
				return nil, err
			}
			return &Credential{Type: CredentialAPIKey, Key: id + strconv.Itoa(i)}, nil
		})
		if err != nil {
			return fmt.Errorf("%s round %d: %w", id, i, err)
		}
	}
	return nil
}

func appendMark(logPath, line string) error {
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(line + "\n")
	return errors.Join(werr, f.Close())
}

// checkAlternating requires the marks to read enter X, leave X in pairs: no enter while another holder is inside.
func checkAlternating(t *testing.T, logPath string, pairs int) {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2*pairs {
		t.Fatalf("got %d marks, want %d:\n%s", len(lines), 2*pairs, data)
	}
	for i := 0; i < len(lines); i += 2 {
		enter, leave := strings.Fields(lines[i]), strings.Fields(lines[i+1])
		if len(enter) != 3 || enter[0] != "enter" || len(leave) != 3 || leave[0] != "leave" || enter[1] != leave[1] || enter[2] != leave[2] {
			t.Fatalf("overlap or interleave at marks %d and %d: %q, %q\n%s", i, i+1, lines[i], lines[i+1], data)
		}
	}
}

// R21 (c)(i): the store file is a link with a RELATIVE target. The lock is made beside the final real file, and the spelling is the chain as written.
func TestR21RelativeLinkLockBesideFinalFile(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"own", "w"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(root, "own", "auth.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "w", "auth.json")
	if err := os.Symlink("../own/auth.json", link); err != nil {
		t.Fatal(err)
	}
	got, err := resolveAuthPath(link)
	if err != nil || got != target {
		t.Fatalf("resolveAuthPath = %q, %v; want %q", got, err, target)
	}
	s, err := NewAuthStorage(link)
	if err != nil {
		t.Fatal(err)
	}
	err = s.withFileLock(t.Context(), false, func(*pilock.Lock) error {
		sameFolderAsLockParent(t, target+".lock", filepath.Join(root, "own"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	setKey(t, s, "p", "fixture-refresh-A")
	if got := fixtureKey(t, s, "p"); got != "fixture-refresh-A" {
		t.Fatalf("store reads %q", got)
	}
}

// R21 (c)(ii): a link to a link to the file, both relative. The lock is made beside the final real file.
func TestR21LinkToLinkRelativeLockBesideFinalFile(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"own", "w", "v"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(root, "own", "auth.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mid := filepath.Join(root, "w", "mid.json")
	top := filepath.Join(root, "v", "auth.json")
	if err := os.Symlink("../own/auth.json", mid); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../w/mid.json", top); err != nil {
		t.Fatal(err)
	}
	got, err := resolveAuthPath(top)
	if err != nil || got != target {
		t.Fatalf("resolveAuthPath = %q, %v; want %q", got, err, target)
	}
	s, err := NewAuthStorage(top)
	if err != nil {
		t.Fatal(err)
	}
	err = s.withFileLock(t.Context(), false, func(*pilock.Lock) error {
		sameFolderAsLockParent(t, target+".lock", filepath.Join(root, "own"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	setKey(t, s, "p", "fixture-refresh-A")
	for _, l := range []string{top, mid} {
		if li, err := os.Lstat(l); err != nil || li.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s is no longer a link: %v", l, err)
		}
	}
}

// R20b: one process, one real store file F, two spellings: A is F's plain path, B reaches F through a folder link. sB holds a stale snapshot when sA writes pa, then sB writes pb. A fresh store on A and a direct read of F's bytes must both hold pa and pb.
func TestR20bTwoSpellingsOneProcessKeepRecords(t *testing.T) {
	for _, mode := range []string{"Set", "Modify"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			realFolder := filepath.Join(root, "owner")
			if err := os.MkdirAll(realFolder, 0o700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(realFolder, "auth.json")
			if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			folderLink := filepath.Join(root, "ownerlink")
			if err := os.Symlink(realFolder, folderLink); err != nil {
				t.Fatal(err)
			}
			spellingA := target
			spellingB := filepath.Join(folderLink, "auth.json")

			sA, err := NewAuthStorage(spellingA)
			if err != nil {
				t.Fatal(err)
			}
			sB, err := NewAuthStorage(spellingB)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := sB.Get("pb"); err != nil { // sB now holds a snapshot that does not yet know pa
				t.Fatal(err)
			}
			if mode == "Set" {
				if err := sA.Set("pa", Credential{Type: CredentialAPIKey, Key: "a1"}); err != nil {
					t.Fatal(err)
				}
				if err := sB.Set("pb", Credential{Type: CredentialAPIKey, Key: "b1"}); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := sA.Modify(t.Context(), "pa", func(*Credential) (*Credential, error) {
					return &Credential{Type: CredentialAPIKey, Key: "a1"}, nil
				}); err != nil {
					t.Fatal(err)
				}
				if _, err := sB.Modify(t.Context(), "pb", func(*Credential) (*Credential, error) {
					return &Credential{Type: CredentialAPIKey, Key: "b1"}, nil
				}); err != nil {
					t.Fatal(err)
				}
			}

			fresh, err := NewAuthStorage(spellingA)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"pa": "a1", "pb": "b1"}
			for _, p := range []string{"pa", "pb"} {
				c, ok, err := fresh.Get(p)
				if err != nil || !ok || c.Key != want[p] {
					t.Errorf("fresh store on A: %s = %+v, %v, %v; want key %s", p, c, ok, err, want[p])
				}
			}
			raw, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			var onDisk map[string]map[string]any
			if err := json.Unmarshal(raw, &onDisk); err != nil {
				t.Fatalf("store bytes: %v", err)
			}
			for p, w := range want {
				if got, _ := onDisk[p]["key"].(string); got != w {
					t.Errorf("file bytes: %s key = %q; want %q", p, got, w)
				}
			}
		})
	}
}
