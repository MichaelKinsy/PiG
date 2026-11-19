package subprocess

import (
	"math"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
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

// The longest path a Host can make is bound for real: the widest runtime directory name os.MkdirTemp can make (h4294967295) and the widest count a uint32 holds (the last path newSocketPath makes), below the deepest socket directory socketRuntimeBase keeps. Its length is the platform's limit, so the operating system accepts every path the budget allows, and the budget is no looser than it must be (a directory one byte deeper is not kept).
func TestTheLongestSocketPathOfTheDeepestKeptDirectoryBindsAtTheLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows sun_path limit is bound by platform_windows_test.go, in a directory a Windows runner has")
	}
	base := testenv.ShortTempDir(t, "pig-long-")
	limit := unixSocketPathLimit(runtime.GOOS)
	deepest := filepath.Join(base, strings.Repeat("d", limit-worstRuntimeSocketSuffix-len(base)-1))
	if len(deepest) != limit-worstRuntimeSocketSuffix {
		t.Fatalf("the deepest kept directory %q has %d bytes, want %d", deepest, len(deepest), limit-worstRuntimeSocketSuffix)
	}
	if got := socketRuntimeBase(runtime.GOOS, deepest, 0, ""); got != deepest {
		t.Fatalf("the deepest directory was not kept: %q", got)
	}
	if got := socketRuntimeBase(runtime.GOOS, deepest+"d", 0, ""); got == deepest+"d" {
		t.Fatalf("a directory one byte deeper was kept: %q", got)
	}
	runtimeDir := filepath.Join(deepest, socketRuntimeDirPrefix+"4294967295")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	host := &Host{socketDir: deepest}
	host.sockRuntimeDir = runtimeDir
	host.sockSeq.Store(lastSocketCount - 1)
	path, err := host.newSocketPath()
	if err != nil {
		t.Fatalf("the longest socket path: %v", err)
	}
	if filepath.Base(path) != socketLeaf(math.MaxUint32) || len(path) != limit {
		t.Fatalf("the longest socket path %q has %d bytes, want %d with the leaf %q", path, len(path), limit, socketLeaf(math.MaxUint32))
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on the longest socket path (%d bytes): %v", len(path), err)
	}
	defer func() { _ = listener.Close() }()
	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			err = conn.Close()
		}
		accepted <- err
	}()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial the longest socket path: %v", err)
	}
	_ = conn.Close()
	if err := <-accepted; err != nil {
		t.Fatalf("accept on the longest socket path: %v", err)
	}
}
