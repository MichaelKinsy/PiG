//go:build !windows

package env

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type probeEvent struct {
	at   time.Duration
	what string
}

type probeTrace struct {
	mu     sync.Mutex
	start  time.Time
	events []probeEvent
}

func (p *probeTrace) note(format string, args ...any) {
	p.mu.Lock()
	p.events = append(p.events, probeEvent{time.Since(p.start), fmt.Sprintf(format, args...)})
	p.mu.Unlock()
}

func (p *probeTrace) String() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var b strings.Builder
	for _, e := range p.events {
		fmt.Fprintf(&b, "    +%9.3fms %s\n", float64(e.at)/1e6, e.what)
	}
	return b.String()
}

var probeRedeliveries, probeRounds, probeHangs atomic.Int64

// probeReadFile is os.ReadFile with a note after each step.
func probeReadFile(name string, tr *probeTrace) outcome {
	tr.note("reader open start")
	f, err := os.Open(name)
	tr.note("reader open returned err=%v", err)
	if err != nil {
		return result("", err)
	}
	defer f.Close()
	data := make([]byte, 0, 512)
	for {
		n, err := f.Read(data[len(data):cap(data)])
		data = data[:len(data)+n]
		tr.note("reader read n=%d err=%v", n, err)
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return result(string(data), err)
		}
		if len(data) >= cap(data) {
			data = append(data, 0)[:len(data)]
		}
	}
}

func probeReadFIFOWithin(fifo string, exited <-chan struct{}, tr *probeTrace, interval, bound time.Duration) (outcome, error) {
	done := make(chan outcome, 1)
	go func() { done <- probeReadFile(fifo, tr) }()
	deadline := time.After(bound)
	select {
	case value := <-done:
		return value, nil
	case <-exited:
		tr.note("select saw writer exited")
	case <-deadline:
		return outcome{}, fmt.Errorf("did not return within %s and its writer had not exited", bound)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case value := <-done:
			tr.note("select saw reader done")
			return value, nil
		case <-ticker.C:
			probeRedeliveries.Add(1)
			tr.note("redeliver start")
			redeliverFIFOEOF(fifo)
			tr.note("redeliver end")
		case <-deadline:
			return outcome{}, fmt.Errorf("did not return within %s although its writer had exited and end of file was signaled again", bound)
		}
	}
}

func TestProbeReadFIFO(t *testing.T) {
	workers, rounds := 8, 1500
	var wg sync.WaitGroup
	failures := make(chan string, workers*4)
	for range workers {
		dir := t.TempDir()
		wg.Go(func() {
			for round := range rounds {
				probeRounds.Add(1)
				tr := &probeTrace{start: time.Now()}
				fifo := filepath.Join(dir, fmt.Sprintf("fifo-%d", round))
				if err := syscall.Mkfifo(fifo, 0o600); err != nil {
					failures <- err.Error()
					return
				}
				exited := make(chan struct{})
				go func() {
					defer close(exited)
					tr.note("writer open start")
					file, err := os.OpenFile(fifo, os.O_WRONLY, 0)
					tr.note("writer open returned err=%v", err)
					if err != nil {
						return
					}
					n, err := file.WriteString("fifo text\n")
					tr.note("writer write n=%d err=%v", n, err)
					err = file.Close()
					tr.note("writer close err=%v", err)
				}()
				value, err := probeReadFIFOWithin(fifo, exited, tr, fifoEOFRedeliveryInterval, 10*time.Second)
				if err != nil {
					failures <- fmt.Sprintf("round %d: %v\n%s", round, err, tr)
					return
				}
				if value.err != nil || value.value != "fifo text\n" {
					failures <- fmt.Sprintf("round %d: read returned a wrong value %q err=%v\n%s", round, value.value, value.err, tr)
					return
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	t.Logf("rounds=%d redeliveries=%d", probeRounds.Load(), probeRedeliveries.Load())
}
