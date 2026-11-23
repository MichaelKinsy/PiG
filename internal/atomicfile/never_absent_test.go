package atomicfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

const (
	t1HelperEnv     = "PIG_ATOMICFILE_T1_OBSERVER"
	t1StopEnv       = "PIG_ATOMICFILE_T1_STOP"
	t1ReadyEnv      = "PIG_ATOMICFILE_T1_READY"
	t1PayloadSize   = 4096
	t1TrailerSize   = 72 // "sha256=" (7) + 64 hex + "\n" (1)
	t1BodySize      = t1PayloadSize - t1TrailerSize
	t1Replacements  = 250
)

func makeT1Payload(seq int) []byte {
	buf := make([]byte, t1PayloadSize)
	header := fmt.Sprintf("seq=%016d ", seq)
	copy(buf, header)
	fill := byte('a' + (seq % 26))
	for i := len(header); i < t1BodySize-1; i++ {
		buf[i] = fill
	}
	buf[t1BodySize-1] = '\n'
	sum := sha256.Sum256(buf[:t1BodySize])
	copy(buf[t1BodySize:], "sha256=")
	hex.Encode(buf[t1BodySize+7:t1PayloadSize-1], sum[:])
	buf[t1PayloadSize-1] = '\n'
	return buf
}

func validT1Payload(data []byte) bool {
	if len(data) != t1PayloadSize || !bytes.HasPrefix(data[t1BodySize:], []byte("sha256=")) || data[t1PayloadSize-1] != '\n' {
		return false
	}
	sum := sha256.Sum256(data[:t1BodySize])
	var want [64]byte
	hex.Encode(want[:], sum[:])
	return bytes.Equal(data[t1BodySize+7:t1PayloadSize-1], want[:])
}

func isWindowsBusyErr(err error) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	// ERROR_ACCESS_DENIED (5), ERROR_SHARING_VIOLATION (32), ERROR_LOCK_VIOLATION (33).
	return errors.Is(err, syscall.Errno(5)) || errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33))
}

func runT1ObserverChild(target, stopPath, readyPath string) (stats string, failed bool) {
	_ = os.WriteFile(readyPath, []byte("ready"), 0o600)
	var statOps, readOps, absent, empty, shortCnt, otherErr int64
	var sample string
	for {
		if _, err := os.Stat(stopPath); err == nil {
			break
		}
		info, err := os.Stat(target)
		statOps++
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				absent++
				if sample == "" {
					sample = "stat: " + err.Error()
				}
			} else if !isWindowsBusyErr(err) {
				otherErr++
				if sample == "" {
					sample = "stat: " + err.Error()
				}
			}
		} else if info.Size() == 0 {
			empty++
		} else if info.Size() != t1PayloadSize {
			shortCnt++
		}

		data, err := os.ReadFile(target)
		readOps++
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				absent++
				if sample == "" {
					sample = "read: " + err.Error()
				}
			} else if !isWindowsBusyErr(err) {
				otherErr++
				if sample == "" {
					sample = "read: " + err.Error()
				}
			}
		} else if len(data) == 0 {
			empty++
		} else if !validT1Payload(data) {
			shortCnt++
		}
	}
	summary := fmt.Sprintf("stat=%d read=%d absent=%d empty=%d short=%d other=%d", statOps, readOps, absent, empty, shortCnt, otherErr)
	if sample != "" {
		summary += fmt.Sprintf(" sample=%q", sample)
	}
	return summary, absent != 0 || empty != 0 || shortCnt != 0 || otherErr != 0
}

// TestWriteFileNeverLeavesPathAbsentAcrossProcessAndGoroutineObservers is T1:
// a writer replacing a 4 KB file through WriteFile in a loop while BOTH a
// same-process goroutine observer AND a separate child process observer loop
// os.Stat and os.ReadFile on the path. On every OS (Linux, macOS, Windows),
// neither observer may ever see the path absent (ENOENT), empty, or short.
func TestWriteFileNeverLeavesPathAbsentAcrossProcessAndGoroutineObservers(t *testing.T) {
	if target := os.Getenv(t1HelperEnv); target != "" {
		summary, failed := runT1ObserverChild(target, os.Getenv(t1StopEnv), os.Getenv(t1ReadyEnv))
		fmt.Println(summary)
		if failed {
			t.Fatalf("child observer saw defect: %s", summary)
		}
		return
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := WriteFile(path, makeT1Payload(0), 0o600); err != nil {
		t.Fatal(err)
	}

	ctrlDir := t.TempDir()
	stopPath := filepath.Join(ctrlDir, "stop")
	readyPath := filepath.Join(ctrlDir, "ready")

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		t1HelperEnv+"="+path,
		t1StopEnv+"="+stopPath,
		t1ReadyEnv+"="+readyPath,
	)
	var childOut bytes.Buffer
	cmd.Stdout = &childOut
	cmd.Stderr = &childOut
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child observer: %v", err)
	}
	stopChild := func() {
		_ = os.WriteFile(stopPath, []byte("stop"), 0o600)
	}
	defer func() {
		stopChild()
		_ = cmd.Wait()
	}()

	// Wait for the child observer to enter its loop without a fixed sleep barrier on the test result.
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if t.Context().Err() != nil {
			t.Fatalf("child observer did not become ready: %v", t.Context().Err())
		}
		runtime.Gosched()
	}

	var (
		done     atomic.Bool
		started  sync.WaitGroup
		observer sync.WaitGroup
		statOps  atomic.Int64
		readOps  atomic.Int64
		absent   atomic.Int64
		empty    atomic.Int64
		shortCnt atomic.Int64
		otherErr atomic.Int64
	)
	started.Add(1)
	observer.Go(func() {
		started.Done()
		for !done.Load() {
			info, err := os.Stat(path)
			statOps.Add(1)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					absent.Add(1)
				} else if !isWindowsBusyErr(err) {
					otherErr.Add(1)
				}
			} else if info.Size() == 0 {
				empty.Add(1)
			} else if info.Size() != t1PayloadSize {
				shortCnt.Add(1)
			}

			data, err := os.ReadFile(path)
			readOps.Add(1)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					absent.Add(1)
				} else if !isWindowsBusyErr(err) {
					otherErr.Add(1)
				}
			} else if len(data) == 0 {
				empty.Add(1)
			} else if !validT1Payload(data) {
				shortCnt.Add(1)
			}
		}
	})
	started.Wait()

	for i := 1; i <= t1Replacements; i++ {
		if err := WriteFile(path, makeT1Payload(i), 0o600); err != nil {
			done.Store(true)
			observer.Wait()
			t.Fatalf("WriteFile %d: %v", i, err)
		}
	}
	done.Store(true)
	observer.Wait()
	stopChild()
	childErr := cmd.Wait()

	if absent.Load() != 0 || empty.Load() != 0 || shortCnt.Load() != 0 || otherErr.Load() != 0 {
		t.Fatalf("goroutine observer across %d replacements (stat=%d read=%d): absent=%d empty=%d short=%d other=%d, want all 0",
			t1Replacements, statOps.Load(), readOps.Load(), absent.Load(), empty.Load(), shortCnt.Load(), otherErr.Load())
	}
	if childErr != nil {
		t.Fatalf("child observer failed: %v\n%s", childErr, strings.TrimSpace(childOut.String()))
	}
}
