package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type flashOp struct {
	Flash      *string `json:"flash,omitempty"`
	Duration   *int    `json:"duration"`
	Dispose    bool    `json:"dispose,omitempty"`
	Expire     bool    `json:"expire,omitempty"`
	Invalidate bool    `json:"invalidate,omitempty"`
	Width      *int    `json:"width,omitempty"`
}

type flashStep struct {
	Rows    []string `json:"rows"`
	Renders int      `json:"renders"`
}

// alt-screen-flash.ts against pinned pi-tui with a manual clock: flash adds an entry and asks for a render, an elapsed timer removes its entry
// and asks again (a negative duration is zero, entries expire independently in any order), dispose clears them without a render, and render
// draws each message in reverse video with a space either side, cut to the width.
func TestAltScreenFlashContainerMatchesPi(t *testing.T) {
	probes := flashProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/alt_screen_flash.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]flashStep
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, ops := range probes {
		got := runFlashProbe(ops)
		if !reflect.DeepEqual(got, expected[i]) {
			if failures++; failures <= 4 {
				t.Errorf("probe %d %s:\n  Pig %+v\n  Pi  %+v", i, mustJSON(ops), got, expected[i])
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// flashClock is a manual clock for the container's removal timers: advance fires every live timer due by then in due-time order.
type flashClock struct {
	now    time.Duration
	seq    int
	timers []*flashTimer
}

type flashTimer struct {
	at   time.Duration
	seq  int
	f    func()
	live bool
}

func (c *flashClock) afterFunc(d time.Duration, f func()) func() bool {
	timer := &flashTimer{at: c.now + d, seq: c.seq, f: f, live: true}
	c.seq++
	c.timers = append(c.timers, timer)
	return func() bool {
		was := timer.live
		timer.live = false
		return was
	}
}

func (c *flashClock) advance(d time.Duration) {
	c.now += d * time.Millisecond
	for {
		var due *flashTimer
		for _, timer := range c.timers {
			if timer.live && timer.at <= c.now && (due == nil || timer.at < due.at || (timer.at == due.at && timer.seq < due.seq)) {
				due = timer
			}
		}
		if due == nil {
			return
		}
		due.live = false
		due.f()
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func flashProbes() [][]flashOp {
	messages := []string{"", "hi", "Saved", "日本語のメッセージ", "a considerably longer flash message than the width", "\x1b[1mbold\x1b[0m text", "🙂 emoji"}
	durations := []*int{nil, intPtr(0), intPtr(1), intPtr(50), intPtr(1000), intPtr(100000), intPtr(-5)}
	widths := []int{0, 1, 2, 3, 5, 10, 80}
	random := rand.New(rand.NewSource(7))
	var probes [][]flashOp
	for range 600 {
		var ops []flashOp
		for range 4 + random.Intn(8) {
			width := widths[random.Intn(len(widths))]
			op := flashOp{Width: &width}
			switch roll := random.Intn(10); {
			case roll < 6:
				message := messages[random.Intn(len(messages))]
				op.Flash, op.Duration = &message, durations[random.Intn(len(durations))]
			case roll == 6:
				op.Dispose = true
			case roll == 7:
				op.Expire = true
			case roll == 8:
				op.Invalidate = true
			default:
				message := messages[random.Intn(len(messages))]
				op.Flash, op.Duration = &message, intPtr(100000)
			}
			ops = append(ops, op)
		}
		probes = append(probes, ops)
	}
	return probes
}

// runFlashProbe replays one probe on Pig's container with a manual clock and returns the rows and render count after every operation.
func runFlashProbe(ops []flashOp) []flashStep {
	var renders int
	container := NewAltScreenFlashContainer(func() { renders++ })
	clock := &flashClock{}
	container.afterFunc = clock.afterFunc
	var got []flashStep
	for _, op := range ops {
		switch {
		case op.Flash != nil:
			duration := altScreenFlashDefaultDurationMS
			if op.Duration != nil {
				duration = *op.Duration
			}
			container.Flash(*op.Flash, duration)
		case op.Dispose:
			container.Dispose()
		case op.Expire:
			clock.advance(2000)
		case op.Invalidate:
			container.Invalidate()
		}
		rows := container.Render(*op.Width)
		if rows == nil {
			rows = []string{}
		}
		got = append(got, flashStep{Rows: rows, Renders: renders})
	}
	return got
}

// TestAltScreenFlashProbeDump prints the corpus for the Pi side of the alt-screen-flash parity scenario.
func TestAltScreenFlashProbeDump(t *testing.T) {
	fmt.Printf("flash-probes:%s\n", mustJSON(flashProbes()))
}

// TestAltScreenFlashParity prints Pig's steps for the corpus, one JSON line per probe, for the alt-screen-flash parity scenario.
func TestAltScreenFlashParity(t *testing.T) {
	for _, ops := range flashProbes() {
		var line bytes.Buffer
		encoder := json.NewEncoder(&line)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(runFlashProbe(ops)); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("flash-observation:%s", line.String())
	}
}

func intPtr(v int) *int { return &v }
