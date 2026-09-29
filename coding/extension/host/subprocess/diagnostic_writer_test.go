package subprocess

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestFactoryDiagnosticSharesLiveLogOffset(t *testing.T) {
	for _, drained := range []bool{false, true} {
		t.Run(map[bool]string{false: "live-copy", true: "drained-copy"}[drained], func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "diagnostic-*.log")
			if err != nil {
				t.Fatal(err)
			}
			log := &processStderrLog{path: file.Name(), writer: file}
			t.Cleanup(log.closeWriter)
			if _, err := file.WriteString("before\n"); err != nil {
				t.Fatal(err)
			}
			if drained {
				log.closeWriter()
			}
			failure := &FactoryLoadError{Message: "factory failed", Stack: "Error: original\n    at factory"}
			if err := log.recordFailure(failure); err != nil {
				t.Fatal(err)
			}
			if !drained {
				if _, err := file.WriteString("after\n"); err != nil {
					t.Fatal(err)
				}
			}
			data, err := os.ReadFile(file.Name())
			want := "before\nfactory failed\nError: original\n    at factory\n"
			if !drained {
				want += "after\n"
			}
			if err != nil || string(data) != want {
				t.Fatalf("diagnostic = %q, %v; want %q", data, err, want)
			}
		})
	}
}

func TestFactoryDiagnosticWriteFailureIsActionable(t *testing.T) {
	log := &processStderrLog{path: filepath.Join(t.TempDir(), "missing.log")}
	me := &managedExt{config: ExtConfig{Name: "broken"}, stderrLog: log}
	failure := me.factoryLoadError(&FactoryLoadError{Message: "factory failed"})
	if !errors.Is(failure, os.ErrNotExist) || !strings.Contains(failure.Error(), "write extension diagnostic") || !strings.Contains(failure.Error(), "factory failed") {
		t.Fatalf("lost diagnostic write error: %v", failure)
	}
}

type heldDiagnosticCopy struct {
	entered chan struct{}
	release chan struct{}
	result  error
	once    sync.Once
	file    *os.File
}

func (w *heldDiagnosticCopy) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	n, err := w.file.Write(data)
	w.result = errors.Join(w.result, err)
	return n, err
}

// Cmd.Wait owns the host's copy goroutines, not just child exit. A diagnostic writer must stay open through their final write.
func TestPackedDiagnosticWriterWaitsForOutputCopy(t *testing.T) {
	nodeCellRequireNode(t)
	file, err := os.CreateTemp(t.TempDir(), "held-copy-*.log")
	if err != nil {
		t.Fatal(err)
	}
	log := &processStderrLog{path: file.Name(), writer: file}
	t.Cleanup(log.closeWriter)
	writer := &heldDiagnosticCopy{entered: make(chan struct{}), release: make(chan struct{}), file: file}
	cmd := exec.CommandContext(t.Context(), "node", "-e", `process.stderr.write("last output\n")`)
	cmd.Stdout, cmd.Stderr = io.Discard, writer
	tree, err := startProcessTree(cmd)
	if err != nil {
		t.Fatal(err)
	}
	process := &packedProcessState{cmd: cmd, processTree: tree, stderrLog: log}
	done := process.startWait()
	defer func() {
		close(writer.release)
		<-done
		if err := writer.result; err != nil {
			t.Errorf("log closed before output copy: %v", err)
		}
		if process.waitErr != nil {
			t.Errorf("process wait: %v", process.waitErr)
		}
		data, err := os.ReadFile(log.path)
		if err != nil || string(data) != "last output\n" {
			t.Errorf("drained log = %q, %v", data, err)
		}
	}()
	select {
	case <-writer.entered:
	case <-done:
		t.Fatal("process exited without reaching its output copy")
	}
	select {
	case <-done:
		t.Fatal("process wait completed while its output copy was held")
	default:
	}
}
