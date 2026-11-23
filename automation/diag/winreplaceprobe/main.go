//go:build windows

// Command winreplaceprobe measures Windows concurrent-reader visibility across
// three write methods: (R) ReplaceFileW with backup (current atomicfile),
// (A1) carryDescriptor + MoveFileEx(REPLACE_EXISTING|WRITE_THROUGH), and
// (A2) in-place writeFileSync form. For each method it runs:
//   - phase=tight: one writer process replacing a 4 KB file against three child
//     reader processes hammering os.Stat, os.ReadFile, and internal/noderealpath.Sync;
//   - phase=openhold: one writer process against a child reader process that opens
//     the file with Go's default os.Open (no FILE_SHARE_DELETE), reads and holds it
//     open 2 to 5 ms, closes, and pauses 5 ms (simulating an editor or AV scanner).
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	method := flag.String("method", "", "R|A1|A2")
	phase := flag.String("phase", "tight", "tight|openhold")
	readerKind := flag.String("reader", "", "stat|readfile|realpath|openhold")
	filePath := flag.String("file", "", "target file path")
	stopPath := flag.String("stop", "", "stop marker file path")
	readyPath := flag.String("ready", "", "reader ready marker path")
	targetWrites := flag.Int("target-writes", 200000, "target replacements per method in tight phase")
	maxDuration := flag.Duration("max-method-duration", 35*time.Minute, "max duration per method in tight phase")
	openHoldWrites := flag.Int("openhold-writes", 1000, "target replacements per method in openhold phase")
	openHoldDuration := flag.Duration("openhold-duration", 5*time.Minute, "max duration per method in openhold phase")
	methodsCSV := flag.String("methods", "R,A1,A2", "comma-separated methods to run")
	flag.Parse()

	switch *role {
	case "coordinator":
		os.Exit(runCoordinator(strings.Split(*methodsCSV, ","), *targetWrites, *maxDuration, *openHoldWrites, *openHoldDuration))
	case "writer":
		os.Exit(runWriter(*method, *phase, *filePath, *targetWrites, *maxDuration))
	case "reader":
		os.Exit(runReader(*method, *phase, *readerKind, *filePath, *stopPath, *readyPath))
	default:
		fmt.Fprintf(os.Stderr, "unknown role: %s\n", *role)
		os.Exit(2)
	}
}

func runCoordinator(methods []string, tightWrites int, tightDuration time.Duration, openHoldWrites int, openHoldDuration time.Duration) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "executable: %v\n", err)
		return 1
	}
	exitCode := 0
	for _, method := range methods {
		method = strings.TrimSpace(method)
		if method == "" {
			continue
		}
		if err := runPhase(exe, method, "tight", []string{"stat", "readfile", "realpath"}, tightWrites, tightDuration); err != nil {
			fmt.Fprintf(os.Stderr, "method=%s phase=tight error: %v\n", method, err)
			exitCode = 1
		}
		if err := runPhase(exe, method, "openhold", []string{"openhold"}, openHoldWrites, openHoldDuration); err != nil {
			fmt.Fprintf(os.Stderr, "method=%s phase=openhold error: %v\n", method, err)
			exitCode = 1
		}
	}
	return exitCode
}

func runPhase(exe, method, phase string, readers []string, targetWrites int, maxDuration time.Duration) error {
	targetDir, err := os.MkdirTemp("", "winreplaceprobe-target-"+method+"-"+phase+"-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(targetDir) }()

	ctrlDir, err := os.MkdirTemp("", "winreplaceprobe-ctrl-"+method+"-"+phase+"-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(ctrlDir) }()

	targetFile := filepath.Join(targetDir, "auth.json")
	f, err := ownerfile.CreateNew(targetFile)
	if err != nil {
		return fmt.Errorf("create initial target: %w", err)
	}
	if _, err := f.Write(makePayload(0)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	beforeSDDL := fileSDDL(targetFile)

	stopFile := filepath.Join(ctrlDir, "stop")
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
				"-method="+method,
				"-phase="+phase,
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
			return fmt.Errorf("start reader %s: %w", kind, err)
		}
		children = append(children, c)
	}

	// Wait until all reader processes have entered their loop.
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
		"-method="+method,
		"-phase="+phase,
		"-file="+targetFile,
		fmt.Sprintf("-target-writes=%d", targetWrites),
		fmt.Sprintf("-max-method-duration=%s", maxDuration),
	)
	var writerOut, writerErr bytes.Buffer
	writerCmd.Stdout = &writerOut
	writerCmd.Stderr = &writerErr
	writeErr := writerCmd.Run()

	// Signal all reader processes to stop and collect their results.
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
			fmt.Fprintf(os.Stderr, "[method=%s phase=%s reader=%s stderr]\n%s", method, phase, c.kind, c.err.String())
		}
		fmt.Print(c.out.String())
	}
	if writerErr.Len() > 0 {
		fmt.Fprintf(os.Stderr, "[method=%s phase=%s writer stderr]\n%s", method, phase, writerErr.String())
	}
	line := strings.TrimRight(writerOut.String(), "\r\n")
	if line != "" {
		fmt.Printf("%s sddl_unchanged=%v\n", line, beforeSDDL == afterSDDL)
	}
	if beforeSDDL != afterSDDL {
		fmt.Fprintf(os.Stderr, "method=%s phase=%s SDDL changed:\n  before: %s\n  after:  %s\n", method, phase, beforeSDDL, afterSDDL)
	}
	return writeErr
}

func runWriter(method, phase, path string, targetWrites int, maxDuration time.Duration) int {
	start := time.Now()
	deadline := start.Add(maxDuration)
	lastProgress := start

	var (
		done            int
		failed          int
		retries         int
		retriedWrites   int
		budgetExhausted int
		worstRetry      time.Duration
		firstErr        string
	)

	for seq := uint64(1); done+failed < targetWrites && time.Now().Before(deadline); seq++ {
		payload := makePayload(seq)
		stats, err := atomicfile.ProbeWriteFile(method, path, payload, 0o600)
		retries += stats.Retries
		if stats.Retries > 0 {
			retriedWrites++
		}
		if stats.RetryWait > worstRetry {
			worstRetry = stats.RetryWait
		}
		if stats.BudgetExhausted {
			budgetExhausted++
		}
		if err != nil {
			failed++
			if firstErr == "" {
				firstErr = err.Error()
			}
		} else {
			done++
		}
		if time.Since(lastProgress) >= 30*time.Second {
			fmt.Fprintf(os.Stderr, "progress method=%s phase=%s done=%d failed=%d retries=%d worst_retry_ms=%d elapsed_s=%.1f\n",
				method, phase, done, failed, retries, worstRetry.Milliseconds(), time.Since(start).Seconds())
			lastProgress = time.Now()
		}
	}

	elapsed := time.Since(start)
	fmt.Printf("PROBE method=%s phase=%s writer=1 done=%d failed=%d retries=%d retried_writes=%d worst_retry_ms=%d budget_exhausted=%d elapsed_s=%.2f",
		method, phase, done, failed, retries, retriedWrites, worstRetry.Milliseconds(), budgetExhausted, elapsed.Seconds())
	if firstErr != "" {
		fmt.Printf(" first_err=%q", firstErr)
	}
	fmt.Println()
	return 0
}

func runReader(method, phase, kind, path, stopPath, readyPath string) int {
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
		if kind == "openhold" || attempted%checkEvery == 0 {
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
		case "openhold":
			// Go's default os.Open opens without FILE_SHARE_DELETE, like an editor or AV scanner.
			// Hold the file open 2 to 5 ms per read, then pause 5 ms before the next read.
			f, err := os.Open(path)
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
				time.Sleep(5 * time.Millisecond)
				continue
			}
			holdTotal := time.Duration(2+(attempted%4)) * time.Millisecond
			time.Sleep(time.Millisecond)
			data, readErr := io.ReadAll(f)
			time.Sleep(holdTotal - time.Millisecond)
			_ = f.Close()
			if readErr != nil {
				switch classifyErr(readErr) {
				case "absent":
					absent++
				case "denied":
					denied++
				default:
					other++
					if sampleErr == "" {
						sampleErr = readErr.Error()
					}
				}
			} else if len(data) == 0 {
				empty++
			} else if !validatePayload(data) {
				shortCnt++
			}
			time.Sleep(5 * time.Millisecond)
		default:
			fmt.Fprintf(os.Stderr, "unknown reader kind: %s\n", kind)
			return 2
		}
	}

	fmt.Printf("PROBE method=%s phase=%s reader=%s attempted=%d absent=%d denied=%d empty=%d short=%d other=%d",
		method, phase, kind, attempted, absent, denied, empty, shortCnt, other)
	if sampleErr != "" {
		fmt.Printf(" sample_err=%q", sampleErr)
	}
	fmt.Println()
	return 0
}
