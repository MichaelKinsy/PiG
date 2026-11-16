package subprocess

import (
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Every call of newSocketPath makes a path no other call made, however many goroutines ask at once, and the longest file name the count can make is the one worstRuntimeSocketLeaf budgets, so the socket directory socketRuntimeBase keeps holds every path. A Host that has made the last path its count allows fails the next call rather than make a path again.
func TestEverySocketPathIsNewAndFitsTheBudget(t *testing.T) {
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test complete") })
	const callers, each = 8, 250
	var (
		mu   sync.Mutex
		seen = map[string]bool{}
		wg   sync.WaitGroup
	)
	for range callers {
		wg.Go(func() {
			for range each {
				path, err := h.newSocketPath()
				mu.Lock()
				if err != nil {
					t.Errorf("newSocketPath: %v", err)
				} else if seen[path] {
					t.Errorf("two calls made the socket path %q", path)
				}
				seen[path] = true
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(seen) != callers*each {
		t.Fatalf("%d calls made %d paths", callers*each, len(seen))
	}
	for path := range seen {
		if filepath.Dir(path) != h.sockRuntimeDir || !strings.HasSuffix(path, socketLeafExtension) {
			t.Fatalf("socket path %q is not a %s file in the runtime directory %q", path, socketLeafExtension, h.sockRuntimeDir)
		}
	}

	// The count at its limit: the last path a uint32 counts is the longest file name, and the call after it fails and leaves the count where it is.
	h.sockSeq.Store(lastSocketCount - 1)
	path, err := h.newSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(path); got != worstRuntimeSocketLeaf {
		t.Fatalf("the last socket file name is %q, the budget names %q", got, worstRuntimeSocketLeaf)
	}
	for range 2 {
		if again, err := h.newSocketPath(); err == nil {
			t.Fatalf("a Host made %q after its last socket path", again)
		}
	}
	if got := socketLeaf(math.MaxUint32); got != worstRuntimeSocketLeaf {
		t.Fatalf("socketLeaf(MaxUint32) = %q, the budget names %q", got, worstRuntimeSocketLeaf)
	}
	for count := uint32(1); count < 100000; count++ {
		if len(socketLeaf(count)) > len(worstRuntimeSocketLeaf) {
			t.Fatalf("socketLeaf(%d) = %q is longer than the budget %q", count, socketLeaf(count), worstRuntimeSocketLeaf)
		}
	}

	for _, goos := range []string{"linux", "darwin", "windows"} {
		sep := "/"
		if goos == "windows" {
			sep = `\`
		}
		// The deepest socket directory socketRuntimeBase keeps for the platform.
		deepest := sep + strings.Repeat("d", unixSocketPathLimit(goos)-worstRuntimeSocketSuffix-1)
		worst := deepest + sep + socketRuntimeDirPrefix + "4294967295" + sep + worstRuntimeSocketLeaf
		if err := validateUnixSocketPath(goos, worst); err != nil {
			t.Fatalf("%s: the longest socket path of the deepest kept directory does not fit: %v", goos, err)
		}
		if got := socketRuntimeBase(goos, deepest, 0, `C:\L`); got != deepest {
			t.Fatalf("%s: the deepest directory was not kept: %q", goos, got)
		}
	}
}
