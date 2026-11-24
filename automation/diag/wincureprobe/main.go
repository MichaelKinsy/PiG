//go:build windows

// Command wincureprobe runs the tight-loop cross-process reader probe against
// the production atomicfile.WriteFile on Windows (the after-cure counterpart
// to diag/windows-replace-probe-2). One writer process replaces a 4 KB file
// in a loop while three separate child reader processes hammer os.Stat,
// os.ReadFile, and internal/noderealpath.Sync.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/atomicfile"
	"github.com/MichaelKinsy/PiG/internal/noderealpath"
	"github.com/MichaelKinsy/PiG/internal/ownerfile"
)

const (
	payloadSize  = 4096
	checksumLine = 72 // "sha256=" (7) + 64 hex + "\n" (1)
	bodySize     = payloadSize - checksumLine
)

func makePayload(seq uint64) []byte {
	buf := make([]byte, payloadSize)
	header := fmt.Sprintf("seq=%016d ", seq)
	copy(buf, header)
	fill := byte('A' + (seq % 26))
	for i := len(header); i < bodySize-1; i++ {
		buf[i] = fill
	}
	buf[bodySize-1] = '\n'
	sum := sha256.Sum256(buf[:bodySize])
	copy(buf[bodySize:], "sha256=")
	hex.Encode(buf[bodySize+7:payloadSize-1], sum[:])
	buf[payloadSize-1] = '\n'
	return buf
}

func validatePayload(data []byte) bool {
	if len(data) != payloadSize {
		return false
	}
	if !bytes.HasPrefix(data[bodySize:], []byte("sha256=")) || data[payloadSize-1] != '\n' {
		return false
	}
	sum := sha256.Sum256(data[:bodySize])
	var want [64]byte
	hex.Encode(want[:], sum[:])
	return bytes.Equal(data[bodySize+7:payloadSize-1], want[:])
}

func classifyErr(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist),
		errors.Is(err, windows.ERROR_FILE_NOT_FOUND),
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
		return "absent"
	case errors.Is(err, windows.ERROR_ACCESS_DENIED),
		errors.Is(err, windows.ERROR_SHARING_VIOLATION),
		errors.Is(err, windows.ERROR_LOCK_VIOLATION),
		errors.Is(err, fs.ErrPermission):
		return "denied"
	default:
		return "other"
	}
}

func fileSDDL(path string) string {
	const parts = windows.OWNER_SECURITY_INFORMATION | windows.GROUP_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, parts)
	if err != nil {
		return "ERR:" + err.Error()
	}
	return sd.String()
}

func main() {
	role := flag.String("role", "coordinator", "coordinator|writer|reader")
	readerKind := flag.String("reader", "", "stat|readfile|realpath")
	filePath := flag.String("file", "", "target file path")
	stopPath := flag.String("stop", "", "stop marker file path")
	readyPath := flag.String("ready", "", "reader ready marker path")
	duration := flag.Duration("duration", 5*time.Minute, "tight-loop run duration")
	flag.Parse()

	switch *role {
	case "coordinator":
		os.Exit(runCoordinator(*duration))
	case "writer":
		os.Exit(runWriter(*filePath, *duration))
	case "reader":
		os.Exit(runReader(*readerKind, *filePath, *stopPath, *readyPath))
	default:
		fmt.Fprintf(os.Stderr, "unknown role: %s\n", *role)
		os.Exit(2)
	}
}

func runCoordinator(duration time.Duration) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "executable: %v\n", err)
		return 1
	}
	targetDir, err := os.MkdirTemp("", "wincureprobe-target-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mkdirtemp target: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(targetDir) }()

	ctrlDir, err := os.MkdirTemp("", "wincureprobe-ctrl-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mkdirtemp ctrl: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(ctrlDir) }()

	targetFile := filepath.Join(targetDir, "auth.json")
	f, err := ownerfile.CreateNew(targetFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create initial target: %v\n", err)
		return 1
	}
	if _, err := f.Write(makePayload(0)); err != nil {
		_ = f.Close()
		fmt.Fprintf(os.Stderr, "write initial target: %v\n", err)
		return 1
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close initial target: %v\n", err)
		return 1
	}
	beforeSDDL := fileSDDL(targetFile)

	stopFile := filepath.Join(ctrlDir, "stop")
	readers := []string{"stat", "readfile", "realpath"}
	type child struct {
		kind string
		cmd  *exec.Cmd
		out  bytes.Buffer
		err  bytes.Buffer
	}
	children := make([]*child, 0, len(readers))
	readyFiles := make([]string, 0, len(readers))

	for _, kind := range readers {
		ready := filepath.Join(ctrlDir, "ready-"+kind)
		readyFiles = append(readyFiles, ready)
		c := &child{
			kind: kind,
			cmd: exec.Command(exe,
				"-role=reader",
				"-reader="+kind,
				"-file="+targetFile,
				"-stop="+stopFile,
				"-ready="+ready,
			),
		}
		c.cmd.Stdout = &c.out
		c.cmd.Stderr = &c.err
		if err := c.cmd.Start(); err != nil {
			_ = os.WriteFile(stopFile, []byte("stop"), 0o600)
			for _, started := range children {
				_ = started.cmd.Wait()
			}
			fmt.Fprintf(os.Stderr, "start reader %s: %v\n", kind, err)
			return 1
		}
		children = append(children, c)
	}

	readyDeadline := time.Now().Add(30 * time.Second)
	for _, ready := range readyFiles {
		for time.Now().Before(readyDeadline) {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	writerCmd := exec.Command(exe,
		"-role=writer",
		"-file="+targetFile,
		fmt.Sprintf("-duration=%s", duration),
	)
	var writerOut, writerErr bytes.Buffer
	writerCmd.Stdout = &writerOut
	writerCmd.Stderr = &writerErr
	writeErr := writerCmd.Run()

	_ = os.WriteFile(stopFile, []byte("stop"), 0o600)
	var wg sync.WaitGroup
	for _, c := range children {
		wg.Go(func() {
			_ = c.cmd.Wait()
		})
	}
	wg.Wait()

	afterSDDL := fileSDDL(targetFile)
	for _, c := range children {
		if c.err.Len() > 0 {
			fmt.Fprintf(os.Stderr, "[reader=%s stderr]\n%s", c.kind, c.err.String())
		}
		fmt.Print(c.out.String())
	}
	if writerErr.Len() > 0 {
		fmt.Fprintf(os.Stderr, "[writer stderr]\n%s", writerErr.String())
	}
	line := strings.TrimRight(writerOut.String(), "\r\n")
	if line != "" {
		fmt.Printf("%s sddl_unchanged=%v\n", line, beforeSDDL == afterSDDL)
	}
	if writeErr != nil || beforeSDDL != afterSDDL {
		return 1
	}
	return 0
}

func runWriter(path string, duration time.Duration) int {
	start := time.Now()
	deadline := start.Add(duration)
	var (
		done         int
		failed       int
		worst        time.Duration
		firstErr     string
		inPlaceCount atomic.Int64
	)
	atomicfile.SetInPlaceReporter(func(string, error) { inPlaceCount.Add(1) })
	defer atomicfile.SetInPlaceReporter(nil)
	for seq := uint64(1); time.Now().Before(deadline); seq++ {
		t0 := time.Now()
		err := atomicfile.WriteFile(path, makePayload(seq), 0o600)
		d := time.Since(t0)
		if d > worst {
			worst = d
		}
		if err != nil {
			failed++
			if firstErr == "" {
				firstErr = err.Error()
			}
		} else {
			done++
		}
	}
	elapsed := time.Since(start)
	fmt.Printf("PROBE method=A1-prod phase=tight writer=1 done=%d failed=%d in_place=%d worst_write_ms=%d elapsed_s=%.2f",
		done, failed, inPlaceCount.Load(), worst.Milliseconds(), elapsed.Seconds())
	if firstErr != "" {
		fmt.Printf(" first_err=%q", firstErr)
	}
	fmt.Println()
	return 0
}

func runReader(kind, path, stopPath, readyPath string) int {
	var (
		attempted uint64
		absent    uint64
		denied    uint64
		empty     uint64
		shortCnt  uint64
		other     uint64
		sampleErr string
	)
	if readyPath != "" {
		_ = os.WriteFile(readyPath, []byte("ready"), 0o600)
	}
	const checkEvery = 256
	for {
		if attempted%checkEvery == 0 {
			if _, err := os.Stat(stopPath); err == nil {
				break
			}
		}
		attempted++
		switch kind {
		case "stat":
			info, err := os.Stat(path)
			if err != nil {
				switch classifyErr(err) {
				case "absent":
					absent++
				case "denied":
					denied++
				default:
					other++
					if sampleErr == "" {
						sampleErr = err.Error()
					}
				}
			} else if info.Size() == 0 {
				empty++
			} else if info.Size() != payloadSize {
				shortCnt++
			}
		case "readfile":
			data, err := os.ReadFile(path)
			if err != nil {
				switch classifyErr(err) {
				case "absent":
					absent++
				case "denied":
					denied++
				default:
					other++
					if sampleErr == "" {
						sampleErr = err.Error()
					}
				}
			} else if len(data) == 0 {
				empty++
			} else if !validatePayload(data) {
				shortCnt++
			}
		case "realpath":
			_, err := noderealpath.Sync(path)
			if err != nil {
				switch classifyErr(err) {
				case "absent":
					absent++
				case "denied":
					denied++
				default:
					other++
					if sampleErr == "" {
						sampleErr = err.Error()
					}
				}
			}
		}
	}
	fmt.Printf("PROBE method=A1-prod phase=tight reader=%s attempted=%d absent=%d denied=%d empty=%d short=%d other=%d",
		kind, attempted, absent, denied, empty, shortCnt, other)
	if sampleErr != "" {
		fmt.Printf(" sample_err=%q", sampleErr)
	}
	fmt.Println()
	return 0
}
