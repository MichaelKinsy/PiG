//go:build linux

package node

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// Closing a connection must not drop the locks another connection of the process holds on the wal-index: POSIX releases
// every lock of a process on a file when any descriptor of it is closed, so the descriptor the token reads through is
// shared and closed only after the last connection to the file has closed.
func TestCommitTokenCloseKeepsTheLocksOfOtherConnections(t *testing.T) {
	if _, err := os.Stat("/proc/locks"); err != nil {
		t.Skip("no /proc/locks:", err)
	}
	path := filepath.Join(t.TempDir(), "locks.sqlite")
	first := openTokenDatabase(t, path)
	if err := first.Exec("CREATE TABLE t (n INTEGER)"); err != nil {
		t.Fatal(err)
	}
	second := openTokenDatabase(t, path)
	if _, err := second.All("SELECT * FROM t"); err != nil {
		t.Fatal(err)
	}
	for _, database := range []*NodeSqliteDatabase{first, second} {
		if err := database.AttachCommitToken(); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(path + "-shm")
	if err != nil {
		t.Fatal(err)
	}
	inode := strconv.FormatUint(info.Sys().(*syscall.Stat_t).Ino, 10)
	held := func() int {
		content, err := os.ReadFile("/proc/locks")
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for line := range strings.SplitSeq(string(content), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 6 && fields[1] == "POSIX" && fields[4] == strconv.Itoa(os.Getpid()) && strings.HasSuffix(fields[5], ":"+inode) {
				count++
			}
		}
		return count
	}
	if held() == 0 {
		t.Fatal("two open connections hold no lock on the wal-index")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if held() == 0 {
		t.Fatal("closing one connection dropped the wal-index locks of the other")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	walIndexes.Lock()
	open := len(walIndexes.files)
	walIndexes.Unlock()
	if open != 0 {
		t.Fatalf("%d wal-index descriptors stay open after the last connection closed", open)
	}
}
