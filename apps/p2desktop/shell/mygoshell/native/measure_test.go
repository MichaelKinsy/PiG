package native

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	brandsprite "github.com/MichaelKinsy/PiG/apps/p2/brand/sprite"
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/egoist/mygo/ui"
)

const fixtureTitle = "fixture-long"

// TestMeasureLong is the chat screen's scroll and stream measurement (smoke/gate/measure.sh runs it with the web client's
// measurement against the same server and session). P2_MEASURE_DATA is a data directory that keeps the fixture: a session of
// P2_MEASURE_EXCHANGES exchanges (a user message and an answer each; 5000 is the 10k-message session), made through
// Chat.send on the first run and reused after. It draws the chat screen on MyGo's tester (software renderer: it times what
// the screen builds and lays out per frame, not a GPU), writes the figures to P2_MEASURE_OUT, then serves the web UI on the
// same data until P2_GATE_DONE_FILE exists, the port and session in P2_GATE_PORT_FILE.
func TestMeasureLong(t *testing.T) {
	data := os.Getenv("P2_MEASURE_DATA")
	if data == "" {
		t.Skip("P2_MEASURE_DATA is not set")
	}
	exchanges, _ := strconv.Atoi(os.Getenv("P2_MEASURE_EXCHANGES"))
	if exchanges == 0 {
		exchanges = 5000
	}
	t.Setenv("P2_DESKTOP_THEME", "dark")
	fake, zero := true, 0
	options := hosts.ServerOptions{DataDir: data, Port: &zero, Fake: &fake, Facets: []host.HostFacet{surfacehost.ServerFacet(nil)}}
	if home, err := brandsprite.DefaultHome(); err == nil {
		if source, err := brandsprite.Open(home); err == nil {
			options.Sprite = source
		}
	}
	server, err := hosts.StartServer(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(p2test.Background) })
	app := New()
	h, err := surfacehost.Open(surfacehost.Options{Server: server.Host, Changed: app.Invalidate})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	if err := app.Start(h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Detach)
	tt := ui.NewTester(app.Build, 1280, 812)
	until(t, tt, "the sessions", app.sessions.Ready)
	id := ""
	find := func() {
		for _, item := range app.sessions.Items("", false) {
			if item.Row != nil && item.Row.Title == fixtureTitle {
				id = item.Row.ID
			}
		}
	}
	find()
	if id == "" {
		var err error
		if id, err = app.sessions.Create(os.TempDir(), fixtureTitle); err != nil {
			t.Fatal(err)
		}
		app.setSel(id)
		until(t, tt, "the chat", func() bool { return app.chat != nil && tt.HasText("Say something to start.") })
		started := time.Now()
		sent := 0
		for sent < exchanges {
			for range min(100, exchanges-sent) {
				if err := app.chat.Send(fmt.Sprintf("message %d: please answer with a short paragraph", sent)); err != nil {
					t.Fatal(err)
				}
				sent++
			}
			deadline := time.Now().Add(10 * time.Minute)
			for time.Now().Before(deadline) {
				tr, _ := app.chat.Transcript()
				if !tr.Busy && len(tr.Queued) == 0 {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Logf("filled %d/%d after %s", sent, exchanges, time.Since(started).Round(time.Second))
		}
	}
	app.setSel("")
	tt.Frame()
	figures := map[string]any{"exchanges": exchanges}
	// Open: the first frame with the transcript and the newest messages.
	opened := time.Now()
	app.setSel(id)
	until(t, tt, "the transcript", func() bool {
		return app.chat != nil && tt.HasText("message "+strconv.Itoa(exchanges-1)+": please answer")
	})
	figures["openMs"] = time.Since(opened).Milliseconds()
	// Scroll back through the history: wheel steps up, each a frame; the older pages load as the top is reached.
	var frames []float64
	scrollStart := time.Now()
	for range 400 {
		tt.Wheel(640, 400, 0, -400)
		tt.Advance(150 * time.Millisecond) // the container glides to its target as the clock advances; not part of the frame
		t0 := time.Now()
		tt.Frame()
		frames = append(frames, float64(time.Since(t0).Microseconds())/1000)
	}
	figures["scrollUp400Ms"] = time.Since(scrollStart).Milliseconds()
	// Where the scroll got to: the oldest numbered message on screen (a scroll that goes nowhere would show the newest).
	figures["oldestVisible"] = oldestMessage(strings.Join(tt.Texts(), "\n"))
	figures["scrollFrameMs"] = summary(frames)
	// Stream: a message while at the end; the frames of the run.
	tt.Key(0, ui.KeyEnd)
	if err := app.chat.Send("stream one more answer"); err != nil {
		t.Fatal(err)
	}
	var run []float64
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); {
		t0 := time.Now()
		tt.Frame()
		run = append(run, float64(time.Since(t0).Microseconds())/1000)
		tr, _ := app.chat.Transcript()
		if !tr.Busy && len(run) > 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	figures["streamFrameMs"] = summary(run)
	var mem runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mem)
	figures["heapMB"] = mem.HeapAlloc >> 20
	if out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output(); err == nil {
		kb, _ := strconv.Atoi(strings.TrimSpace(string(out)))
		figures["rssMB"] = kb >> 10
	}
	encoded, _ := json.MarshalIndent(figures, "", "  ")
	if path := os.Getenv("P2_MEASURE_OUT"); path != "" {
		_ = os.WriteFile(path, encoded, 0o600)
	}
	t.Logf("%s", encoded)
	if portFile := os.Getenv("P2_GATE_PORT_FILE"); portFile != "" {
		_ = os.WriteFile(portFile, []byte(strconv.Itoa(server.Port)+"\n"+id), 0o600)
		deadline := time.Now().Add(30 * time.Minute)
		for done := os.Getenv("P2_GATE_DONE_FILE"); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
			if _, err := os.Stat(done); err == nil {
				break
			}
		}
	}
}

// summary is the mean, the 95th percentile and the worst of a list of frame times (ms).
func summary(v []float64) map[string]float64 {
	if len(v) == 0 {
		return nil
	}
	sorted := slices.Clone(v)
	slices.Sort(sorted)
	sum := 0.0
	for _, x := range v {
		sum += x
	}
	return map[string]float64{"n": float64(len(v)), "mean": round(sum / float64(len(v))), "p95": round(sorted[len(sorted)*95/100]), "max": round(sorted[len(sorted)-1])}
}

func round(x float64) float64 { return float64(int(x*100)) / 100 }

// oldestMessage is the lowest n of the "message n: please answer" texts in text, -1 when there is none.
func oldestMessage(text string) int {
	oldest := -1
	for _, found := range regexp.MustCompile(`message (\d+): please answer`).FindAllStringSubmatch(text, -1) {
		if n, err := strconv.Atoi(found[1]); err == nil && (oldest < 0 || n < oldest) {
			oldest = n
		}
	}
	return oldest
}
