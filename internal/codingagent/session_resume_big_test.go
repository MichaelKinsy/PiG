package codingagent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestSessionListingBigSynthetic lists a 300+ MB session plus 160 small ones: rows and order must equal the full-decode reference. Set PIG_BIG_SESSION_TEST=1 to run it.
func TestSessionListingBigSynthetic(t *testing.T) {
	if os.Getenv("PIG_BIG_SESSION_TEST") == "" {
		t.Skip("set PIG_BIG_SESSION_TEST=1")
	}
	dir := t.TempDir()
	var files []string
	big := filepath.Join(dir, "big.jsonl")
	f, _ := os.Create(big)
	w := bufio.NewWriterSize(f, 1<<20)
	_, _ = fmt.Fprintln(w, fastTestHeader)
	pad := strings.Repeat("lorem <ipsum> ", 3000)
	for i := 0; w.Buffered() >= 0 && i < 24000; i++ {
		_, _ = fmt.Fprintf(w, `{"type":"message","id":"t%d","message":{"role":"toolResult","content":[{"type":"text","text":"%s"}]}}`+"\n", i, pad)
		if i%2 == 0 {
			_, _ = fmt.Fprintf(w, `{"type":"message","id":"a%d","message":{"role":"assistant","content":[{"type":"text","text":"reply %d %s"}],"timestamp":%d}}`+"\n", i, i, pad, 1700000000000+i)
		}
	}
	_ = w.Flush()
	_ = f.Close()
	files = append(files, big)
	for i := range 160 {
		p := filepath.Join(dir, fmt.Sprintf("s%03d.jsonl", i))
		body := fastTestHeader + "\n" + fmt.Sprintf(`{"type":"message","id":"u","message":{"role":"user","content":"hi %d"}}`, i) + "\n"
		_ = os.WriteFile(p, []byte(body), 0o600)
		files = append(files, p)
	}
	if st, _ := os.Stat(big); st.Size() < 300<<20 {
		t.Fatalf("big only %d MB", st.Size()>>20)
	}
	var want []SessionInfo
	t0 := time.Now()
	_, _ = summarizeSessionFileFull(context.Background(), big)
	t.Logf("baseline full serial decode of big file: %d ms", time.Since(t0).Milliseconds())
	for _, p := range files {
		if info, err := summarizeSessionFileFull(context.Background(), p); err == nil {
			want = append(want, info)
		}
	}
	slicesSortInfos(want)
	run := func(label string) []SessionInfo {
		var ms0, ms1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&ms0)
		peak := uint64(0)
		stop := make(chan struct{})
		go func() {
			var m runtime.MemStats
			for {
				select {
				case <-stop:
					return
				case <-time.After(5 * time.Millisecond):
					runtime.ReadMemStats(&m)
					peak = max(peak, m.HeapAlloc)
				}
			}
		}()
		start := time.Now()
		got, _ := summarizeSessionFilesWithOptions(files, sessionListOptions(nil), 10, false)
		el := time.Since(start)
		close(stop)
		runtime.ReadMemStats(&ms1)
		t.Logf("%s: %d ms, peak heap %d MB", label, el.Milliseconds(), peak>>20)
		return got
	}
	cold := run("cold")
	warm := run("warm")
	if !reflect.DeepEqual(cold, want) || !reflect.DeepEqual(warm, want) {
		t.Fatal("listing differs from full-decode reference")
	}
}

func slicesSortInfos(s []SessionInfo) { slices.SortStableFunc(s, compareSessionModifiedDesc) }
