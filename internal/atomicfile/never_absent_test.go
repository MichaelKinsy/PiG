package atomicfile

import (
	"bytes"
	"context"
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
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const (
	t1HelperEnv     = "PIG_ATOMICFILE_T1_OBSERVER"
	t1StopEnv       = "PIG_ATOMICFILE_T1_STOP"
	t1ReadyEnv      = "PIG_ATOMICFILE_T1_READY"
	t1PayloadSize   = 4096
	t1TrailerSize   = 72 // "sha256=" (7) + 64 hex + "\n" (1)
	t1BodySize      = t1PayloadSize - t1TrailerSize
	t1Replacements  = 100
	t1ObserverPause = 1 * time.Millisecond
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

type t1ObserverCounts struct {
	statOps  int64
	readOps  int64
	absent   int64
	denied   int64
	empty    int64
	shortCnt int64
	otherErr int64
	sample   string
}

func runT1ObserverChild(target, stopPath, readyPath string) (stats string, c t1ObserverCounts) {
	_ = os.WriteFile(readyPath, []byte("ready"), 0o600)
	ticker := time.NewTicker(t1ObserverPause)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(stopPath); err == nil {
			break
		}
		info, err := os.Stat(target)
		c.statOps++
		switch {
		case err != nil:
			switch {
			case errors.Is(err, fs.ErrNotExist):
				c.absent++
				if c.sample == "" {
					c.sample = "stat: " + err.Error()
				}
			case isWindowsBusyErr(err):
				c.denied++
			default:
				c.otherErr++
				if c.sample == "" {
					c.sample = "stat: " + err.Error()
				}
			}
		case info.Size() == 0:
			c.empty++
		case info.Size() != t1PayloadSize:
			c.shortCnt++
		}

		data, err := os.ReadFile(target)
		c.readOps++
		switch {
		case err != nil:
			switch {
			case errors.Is(err, fs.ErrNotExist):
				c.absent++
				if c.sample == "" {
					c.sample = "read: " + err.Error()
				}
			case isWindowsBusyErr(err):
				c.denied++
			default:
				c.otherErr++
				if c.sample == "" {
					c.sample = "read: " + err.Error()
				}
			}
		case len(data) == 0:
			c.empty++
		case !validT1Payload(data):
			c.shortCnt++
		}
		<-ticker.C
	}
	summary := fmt.Sprintf("stat=%d read=%d absent=%d denied=%d empty=%d short=%d other=%d",
		c.statOps, c.readOps, c.absent, c.denied, c.empty, c.shortCnt, c.otherErr)
	if c.sample != "" {
		summary += fmt.Sprintf(" sample=%q", c.sample)
	}
	return summary, c
}

// TestWriteFileNeverLeavesPathAbsentAcrossProcessAndGoroutineObservers is T1:
// a writer replacing a 4 KB file through WriteFile in a loop while BOTH a
// same-process goroutine observer AND a separate child process observer poll
// os.Stat and os.ReadFile on the path with a brief pause between reads (the
// spin-loop stress probe lives in the diagnostic workflow). On every OS (Linux,
// macOS, Windows), neither observer may ever see the path absent (ENOENT) and
// every WriteFile returns nil; empty and short are asserted 0 when no in-place
// fallback occurred (the reporter count is logged either way).
func TestWriteFileNeverLeavesPathAbsentAcrossProcessAndGoroutineObservers(t *testing.T) {
	if target := os.Getenv(t1HelperEnv); target != "" {
		summary, counts := runT1ObserverChild(target, os.Getenv(t1StopEnv), os.Getenv(t1ReadyEnv))
		fmt.Println(summary)
		if counts.absent != 0 || counts.otherErr != 0 {
			t.Fatalf("child observer saw absent/other: %s", summary)
		}
		return
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := WriteFile(path, makeT1Payload(0), 0o600); err != nil {
		t.Fatal(err)
	}

	var inPlaceCount atomic.Int64
	SetInPlaceReporter(func(string, error) { inPlaceCount.Add(1) })
	t.Cleanup(func() { SetInPlaceReporter(nil) })

	ctrlDir := t.TempDir()
	stopPath := filepath.Join(ctrlDir, "stop")
	readyPath := filepath.Join(ctrlDir, "ready")

	waitBudget := testbudget.Wait(t)
	ctx, cancel := context.WithTimeout(t.Context(), waitBudget)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
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

	readyDeadline := time.NewTimer(waitBudget)
	defer readyDeadline.Stop()
	readyTick := time.NewTicker(1 * time.Millisecond)
	defer readyTick.Stop()
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("child observer did not become ready: %v", ctx.Err())
		case <-readyDeadline.C:
			t.Fatalf("child observer did not become ready within %v", waitBudget)
		case <-readyTick.C:
		}
	}

	var (
		done     atomic.Bool
		started  sync.WaitGroup
		observer sync.WaitGroup
		statOps  atomic.Int64
		readOps  atomic.Int64
		absent   atomic.Int64
		denied   atomic.Int64
		empty    atomic.Int64
		shortCnt atomic.Int64
		otherErr atomic.Int64
	)
	started.Add(1)
	observer.Go(func() {
		started.Done()
		ticker := time.NewTicker(t1ObserverPause)
		defer ticker.Stop()
		for !done.Load() {
			info, err := os.Stat(path)
			statOps.Add(1)
			switch {
			case err != nil:
				switch {
				case errors.Is(err, fs.ErrNotExist):
					absent.Add(1)
				case isWindowsBusyErr(err):
					denied.Add(1)
				default:
					otherErr.Add(1)
				}
			case info.Size() == 0:
				empty.Add(1)
			case info.Size() != t1PayloadSize:
				shortCnt.Add(1)
			}

			data, err := os.ReadFile(path)
			readOps.Add(1)
			switch {
			case err != nil:
				switch {
				case errors.Is(err, fs.ErrNotExist):
					absent.Add(1)
				case isWindowsBusyErr(err):
					denied.Add(1)
				default:
					otherErr.Add(1)
				}
			case len(data) == 0:
				empty.Add(1)
			case !validT1Payload(data):
				shortCnt.Add(1)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
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

	childText := strings.TrimSpace(childOut.String())
	inPlace := inPlaceCount.Load()
	t.Logf("in_place=%d goroutine observer: stat=%d read=%d absent=%d denied=%d empty=%d short=%d other=%d; child=%s",
		inPlace, statOps.Load(), readOps.Load(), absent.Load(), denied.Load(), empty.Load(), shortCnt.Load(), otherErr.Load(), childText)
	if absent.Load() != 0 || otherErr.Load() != 0 {
		t.Fatalf("goroutine observer across %d replacements (in_place=%d): absent=%d denied=%d empty=%d short=%d other=%d, want absent/other 0",
			t1Replacements, inPlace, absent.Load(), denied.Load(), empty.Load(), shortCnt.Load(), otherErr.Load())
	}
	if inPlace == 0 {
		if empty.Load() != 0 || shortCnt.Load() != 0 {
			t.Fatalf("goroutine observer with in_place=0: empty=%d short=%d, want 0", empty.Load(), shortCnt.Load())
		}
		if !strings.Contains(childText, "empty=0 short=0") {
			t.Fatalf("child observer with in_place=0 saw empty/short: %s", childText)
		}
	}
	if childErr != nil {
		t.Fatalf("child observer failed: %v\n%s", childErr, childText)
	}
}
