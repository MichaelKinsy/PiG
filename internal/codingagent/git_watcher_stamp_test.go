package codingagent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// RT-F12: the reftable poll stats tables.list every 250 ms for the life of an interactive session (upstream fs.watchFile), so a poll is one stat: the reflection and fmt read of the change time cost 4 allocations per poll; syscall.Stat's copy of the path is the one left.
func TestGitPathStampAllocationBudget(t *testing.T) {
	if !gitStampHasChangeTime {
		t.Skip("this platform stats through os.Stat")
	}
	path := filepath.Join(t.TempDir(), "tables.list")
	if err := os.WriteFile(path, []byte("0x000000000001-0x000000000001-00000000.ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if allocs := testing.AllocsPerRun(100, func() { _ = gitPathStamp(path) }); allocs > 1 {
		t.Fatalf("a reftable poll allocates %.1f times, want at most 1", allocs)
	}
}

// The stamp changes with the modification time, the size and the change time (a chmod changes only the last), and reads zero for a missing file, as the reflection-based stamp did.
func TestGitPathStampSeesEveryChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tables.list")
	if got := gitPathStamp(path); got != (gitStatStamp{}) {
		t.Fatalf("missing file stamp = %+v, want zero", got)
	}
	if err := os.WriteFile(path, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created := gitPathStamp(path)
	if created == (gitStatStamp{}) {
		t.Fatal("existing file stamp is zero")
	}
	past := time.Unix(1_000_000_000, 0)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	touched := gitPathStamp(path)
	if touched.modified == created.modified {
		t.Fatalf("mtime change not seen: %+v -> %+v", created, touched)
	}
	if err := os.WriteFile(path, []byte("ab\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	grown := gitPathStamp(path)
	if grown.size == touched.size || grown.modified != touched.modified {
		t.Fatalf("size change not seen alone: %+v -> %+v", touched, grown)
	}
	if !gitStampHasChangeTime {
		return
	}
	// The change time has a 1 s or coarser granularity on some file systems.
	deadline := time.Now().Add(3 * time.Second)
	for mode := os.FileMode(0o600); ; mode ^= 0o044 {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if next := gitPathStamp(path); next.changed != grown.changed {
			if next.modified != grown.modified || next.size != grown.size {
				t.Fatalf("chmod changed more than the change time: %+v -> %+v", grown, next)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("chmod did not change the change time")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
