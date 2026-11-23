//go:build !windows

package atomicfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/fsretry"
)

func moveTestFiles(t *testing.T) (path, temp string) {
	t.Helper()
	dir := t.TempDir()
	path, temp = filepath.Join(dir, "auth.json"), filepath.Join(dir, "auth.json.tmp-1")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, temp
}

func readFileOrErr(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(data)
}

// TestReplaceWithMoveCarriesDescriptorBeforeMoveWithBothFlags proves the A1
// Windows replacement contract on Linux through the step seam: for an existing
// file, the descriptor carry runs BEFORE the single MoveFileExW call, and the
// move passes MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH (0x9).
func TestReplaceWithMoveCarriesDescriptorBeforeMoveWithBothFlags(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existed  bool
		carryErr error
		wantLog  []string
	}{
		{"existing file carries then moves", true, nil, []string{"carry", "move"}},
		{"existing file tolerates auto-inherited diff", true, errAutoInheritedDiffers, []string{"carry", "move"}},
		{"new file moves without carry", false, nil, []string{"move"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, temp := moveTestFiles(t)
			var order []string
			var gotFlags uint32
			moveCalls := 0
			err := replaceWithMove(t.Context(), temp, path, tc.existed, windowsMoveSteps{
				carry: func(from, to string) error {
					if from != path || to != temp {
						t.Errorf("carry(%q, %q), want (%q, %q)", from, to, path, temp)
					}
					order = append(order, "carry")
					return tc.carryErr
				},
				move: func(from, to string, flags uint32) error {
					moveCalls++
					gotFlags = flags
					order = append(order, "move")
					return os.Rename(from, to)
				},
				retry: func(_ context.Context, op func() error) error { return op() },
			})
			if err != nil {
				t.Fatalf("replaceWithMove = %v, want nil", err)
			}
			if !slices.Equal(order, tc.wantLog) {
				t.Fatalf("step order = %v, want %v", order, tc.wantLog)
			}
			const wantFlags = moveReplaceExisting | moveWriteThrough
			if moveCalls != 1 || gotFlags != wantFlags {
				t.Fatalf("move called %d times with flags 0x%x, want 1 call with 0x%x", moveCalls, gotFlags, wantFlags)
			}
			if got := readFileOrErr(path); got != "new" {
				t.Fatalf("file at path = %q, want new", got)
			}
		})
	}
}

// TestReplaceWithMoveStopsBeforeMoveWhenCarryFails proves that if carrying the
// existing file's access fails with anything other than errAutoInheritedDiffers,
// the move is never attempted and the old file stays untouched.
func TestReplaceWithMoveStopsBeforeMoveWhenCarryFails(t *testing.T) {
	path, temp := moveTestFiles(t)
	moved := false
	err := replaceWithMove(t.Context(), temp, path, true, windowsMoveSteps{
		carry: func(string, string) error { return errAccessDiffers },
		move: func(string, string, uint32) error {
			moved = true
			return nil
		},
		retry: func(_ context.Context, op func() error) error { return op() },
	})
	if !errors.Is(err, errAccessDiffers) {
		t.Fatalf("replaceWithMove = %v, want %v", err, errAccessDiffers)
	}
	if moved {
		t.Fatal("move was called after descriptor carry failed")
	}
	if got := readFileOrErr(path); got != "old" {
		t.Fatalf("file at path = %q, want old untouched", got)
	}
}

// TestReplaceWithMoveRetriesRefusedMoveInsideBudgetAndReportsOnExhaustion proves
// that transient Windows sharing/access refusals retry inside fsretry.RenameBudget
// and succeed once the hold ends, while an unclosed hold exhausts the budget,
// returns the refusal, leaves the old file whole, and lets WriteFile remove temp.
func TestReplaceWithMoveRetriesRefusedMoveInsideBudgetAndReportsOnExhaustion(t *testing.T) {
	transient := func(err error) bool {
		return errors.Is(err, syscall.Errno(5)) || errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33))
	}

	t.Run("transient refusal succeeds inside budget", func(t *testing.T) {
		path, temp := moveTestFiles(t)
		attempts := 0
		var waits []time.Duration
		err := replaceWithMove(t.Context(), temp, path, true, windowsMoveSteps{
			carry: func(string, string) error { return nil },
			move: func(from, to string, flags uint32) error {
				if flags != moveReplaceExisting|moveWriteThrough {
					t.Errorf("flags = 0x%x, want 0x%x", flags, moveReplaceExisting|moveWriteThrough)
				}
				attempts++
				if attempts <= 2 {
					return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.Errno(32)}
				}
				return os.Rename(from, to)
			},
			retry: func(ctx context.Context, op func() error) error {
				return retryWithWait(ctx, fsretry.RenameBudget, fsretry.RenameFirstDelay, fsretry.RenameMaxDelay, transient,
					func(_ context.Context, d time.Duration) error {
						waits = append(waits, d)
						return nil
					}, op)
			},
		})
		if err != nil {
			t.Fatalf("replaceWithMove = %v, want nil after transient hold clears", err)
		}
		if attempts != 3 || len(waits) != 2 || waits[0] != fsretry.RenameFirstDelay || waits[1] != 2*fsretry.RenameFirstDelay {
			t.Fatalf("attempts=%d waits=%v, want 3 attempts and two doubling waits", attempts, waits)
		}
		if got := readFileOrErr(path); got != "new" {
			t.Fatalf("file at path = %q, want new", got)
		}
	})

	t.Run("exhausted budget leaves old file whole and WriteFile deletes temp", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "auth.json")
		if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		var totalWait time.Duration
		previousReplace := replaceTemp
		replaceTemp = func(temp, target string, existed bool) error {
			return replaceWithMove(t.Context(), temp, target, existed, windowsMoveSteps{
				carry: func(string, string) error { return nil },
				move: func(from, to string, _ uint32) error {
					return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.Errno(5)}
				},
				retry: func(ctx context.Context, op func() error) error {
					return retryWithWait(ctx, fsretry.RenameBudget, fsretry.RenameFirstDelay, fsretry.RenameMaxDelay, transient,
						func(_ context.Context, d time.Duration) error {
							totalWait += d
							return nil
						}, op)
				},
			})
		}
		t.Cleanup(func() { replaceTemp = previousReplace })

		err := WriteFile(path, []byte("new"), 0o600)
		if !errors.Is(err, syscall.Errno(5)) {
			t.Fatalf("WriteFile = %v, want ERROR_ACCESS_DENIED (5)", err)
		}
		if totalWait != fsretry.RenameBudget {
			t.Fatalf("total retry wait = %v, want %v", totalWait, fsretry.RenameBudget)
		}
		if got := readFileOrErr(path); got != "old" {
			t.Fatalf("file at path = %q, want old intact", got)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != "auth.json" {
			t.Fatalf("directory holds %d entries after failed replace, want only auth.json (temp must be removed)", len(entries))
		}
	})
}

// carryVerdict: the entries with their flags and SE_DACL_PROTECTED are the access; SE_DACL_AUTO_INHERITED alone is not.
func TestCarryVerdict(t *testing.T) {
	base := access{protected: true, entries: []string{"allow-owner"}}
	for _, tc := range []struct {
		name string
		got  access
		want error
	}{
		{"equal", base, nil},
		{"only the auto-inherited flag", access{protected: true, autoInherited: true, entries: []string{"allow-owner"}}, errAutoInheritedDiffers},
		{"other entries", access{protected: true, entries: []string{"allow-everyone"}}, errAccessDiffers},
		{"an entry more", access{protected: true, entries: []string{"allow-owner", "allow-everyone"}}, errAccessDiffers},
		{"other entry flags", access{protected: true, entries: []string{"allow-owner-inherit"}}, errAccessDiffers},
		{"not protected", access{entries: []string{"allow-owner"}}, errAccessDiffers},
		{"auto-inherited and other entries", access{protected: true, autoInherited: true, entries: []string{"x"}}, errAccessDiffers},
		{"a NULL DACL", access{protected: true, null: true}, errAccessDiffers},
		{"no DACL", access{protected: true, absent: true}, errAccessDiffers},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := carryVerdict(base, tc.got); !errors.Is(got, tc.want) || (tc.want == nil) != (got == nil) {
				t.Fatalf("carryVerdict = %v, want %v", got, tc.want)
			}
		})
	}
}

// The owner and the group are carried as far as the token may set them: the refusals of a token without the right are not errors, every other error is.
func TestIgnoreOwnerRefusal(t *testing.T) {
	for _, code := range []syscall.Errno{errInvalidOwner, errInvalidPrimaryGroup, errPrivilegeNotHeld, errAccessDenied} {
		t.Run(code.Error(), func(t *testing.T) {
			if err := ignoreOwnerRefusal(&os.PathError{Op: "set owner", Path: "x", Err: code}); err != nil {
				t.Fatalf("error = %v, want it ignored", err)
			}
		})
	}
	sharing := syscall.Errno(32) // ERROR_SHARING_VIOLATION
	if err := ignoreOwnerRefusal(&os.PathError{Op: "set owner", Path: "x", Err: sharing}); !errors.Is(err, sharing) {
		t.Fatalf("error = %v, want the sharing violation returned", err)
	}
	if err := ignoreOwnerRefusal(nil); err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
}
