// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package tui

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/screenwriter"
	"github.com/MichaelKinsy/PiG/internal/screenwriter/gridterm"
)

// screenWriterReplay is one stream of the renderers' bytes with a screen writer (D624) under it, and the screens the stream leaves
// with and without the writer on every width model of gridterm.
type screenWriterReplay struct {
	name        string
	size        [2]int
	writer      *screenwriter.Writer
	pair        *gridterm.Pair
	differences []string
}

func newScreenWriterReplay(name string, cols, rows int) *screenWriterReplay {
	r := &screenWriterReplay{name: name, size: [2]int{cols, rows}, pair: gridterm.NewPair(cols, rows)}
	r.writer = screenwriter.New(screenwriter.Options{Size: func() (int, int) { return r.size[0], r.size[1] }, Settle: -1})
	return r
}

// translate is what the writer sends for a step of the stream, after the terminal changed size to resize when set: the step's
// writes translated one by one, and after every seventh step the settle write.
func (r *screenWriterReplay) translate(index int, resize []int, out string) string {
	if resize != nil {
		r.size = [2]int{resize[0], resize[1]}
	}
	var translated strings.Builder
	for _, write := range gridterm.SplitWrites(out) {
		translated.WriteString(r.writer.Translate(write))
	}
	if index%7 == 6 {
		translated.WriteString(r.writer.Settled())
	}
	return translated.String()
}

// step feeds one step of the stream and compares the screens.
func (r *screenWriterReplay) step(index int, resize []int, out string) {
	translated := r.translate(index, resize, out)
	if resize != nil {
		r.pair.Resize(resize[0], resize[1])
	}
	if d := r.pair.Step(out, translated); d != "" && len(r.differences) == 0 {
		r.differences = append(r.differences, fmt.Sprintf("%s step %d: %s\n  renderer wrote %q\n  writer wrote   %q", r.name, index, d, out, translated))
	}
}

// screenWriterStreams are the two recorded steps of the benchmark the screen writer's package keeps (what PiG wrote for the long
// answer and for the 50-file write on a 160 by 48 terminal), each cut into its writes, the alternate screen entered first.
func screenWriterStreams(t *testing.T) map[string][]string {
	streams := map[string][]string{}
	for _, name := range []string{"race-md20000", "race-write50"} {
		file, err := os.Open("../internal/screenwriter/testdata/" + name + ".stream.gz")
		if err != nil {
			t.Fatal(err)
		}
		reader, err := gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		streams[name] = gridterm.SplitWrites("\x1b[?1049h" + string(data))
	}
	return streams
}

// Whatever the alternate screen writes for the frames of the terminal-engine corpus and of the pinned Pi's fixture (Pi's bytes, and
// PiG's with the rows that moved scrolled), the screen writer's bytes leave the same cells, pen, cursor and scroll region on a terminal
// after every frame and after the settle write, on four terminals that count cluster widths differently. And the frame grammar the
// writer reads is the one Pi's doRender writes: of the frames of Pi's own 500 scenarios, the ones it has no rule for are the ones
// that hold an image, a line feed inside a line or a screen narrower than two columns, and their number is pinned, so a frame Pi
// starts to write another way shows here.
func TestScreenWriterShowsTheRenderersScreens(t *testing.T) {
	var streams, steps, differences int
	var total, pis screenwriter.Stats
	add := func(r *screenWriterReplay, into *screenwriter.Stats) {
		streams++
		steps += r.pair.Steps
		differences += r.pair.Differences
		s := r.writer.Stats()
		into.Frames += s.Frames
		into.Translated += s.Translated
		into.Unchanged += s.Unchanged
		into.Repaints += s.Repaints
		into.Unread += s.Unread
		into.Settles += s.Settles
		into.BytesIn += s.BytesIn
		into.BytesOut += s.BytesOut
		for _, d := range r.differences {
			t.Error(d)
		}
	}
	scenarios := slices.Concat(termCorpusScenarios, termKnownScenarios)
	for seed := range uint64(6) {
		scenarios = append(scenarios, termRandomMix(seed))
	}
	for _, name := range []string{"stream-follow", "resize-mid-stream", "wide-styled", "random-mix-2"} {
		index := slices.IndexFunc(scenarios, func(sc termScenario) bool { return sc.name == name })
		unclipped := scenarios[index]
		unclipped.name, unclipped.unclipped = "unclipped-"+name, true
		scenarios = append(scenarios, unclipped)
	}
	for _, sc := range scenarios {
		for _, scrollRows := range []bool{true, false} {
			variant := &termCorpusVariant{}
			t.Run(fmt.Sprint(sc.name, "/scroll=", scrollRows), func(t *testing.T) { playTermScenario(t, sc, &termRecorder{variant}, scrollRows) })
			replay := newScreenWriterReplay(fmt.Sprint(sc.name, " scroll=", scrollRows), sc.cols, sc.rows)
			start := 0
			for i, step := range variant.Steps {
				replay.step(i, step.Resize, string(variant.data[start:step.End]))
				start = step.End
			}
			add(replay, &total)
		}
	}
	for _, variant := range []string{"A", "B"} {
		var replay *screenWriterReplay
		into := &total
		if variant == "B" {
			into = &pis
		}
		index := 0
		tap := &oracleTap{
			recordOnly: true,
			start: func(scenario, cols, rows int, _ string, out string) {
				if replay != nil {
					add(replay, into)
				}
				replay, index = newScreenWriterReplay(fmt.Sprint("oracle-", scenario, " ", variant), cols, rows), 0
				replay.step(index, nil, out)
			},
			step: func(_ int, resize []int, _ bool, out string) {
				index++
				replay.step(index, resize, out)
			},
		}
		t.Run("oracle/"+variant, func(t *testing.T) {
			if variant == "A" {
				checkAltScreenOracleIn(t, true, nil, tap)
			} else {
				checkAltScreenOracleIn(t, false, map[string]string{"PIG_TUI_SCROLL_ROWS": "0"}, tap)
			}
		})
		if replay != nil {
			add(replay, into)
		}
	}
	for name, writes := range screenWriterStreams(t) {
		replay := newScreenWriterReplay(name, 160, 48)
		for i, write := range writes {
			replay.step(i, nil, write)
		}
		add(replay, &total)
	}
	t.Logf("%d streams, %d steps, %d differences", streams, steps, differences)
	t.Logf("Pi's bytes of the fixture: %+v", pis)
	t.Logf("the other streams: %+v", total)
	if total.Translated+pis.Translated < 5000 || total.Unchanged+pis.Unchanged < 1000 || total.Settles+pis.Settles < 500 {
		t.Errorf("the streams do not exercise the writer: %d frames translated, %d not written, %d settle writes", total.Translated+pis.Translated, total.Unchanged+pis.Unchanged, total.Settles+pis.Settles)
	}
	if pis.Frames != screenWriterPisFrames || pis.Unread != screenWriterPisUnread {
		t.Errorf("of %d frames of Pi's fixture the writer has no rule for %d; it was %d of %d when the frame grammar was last reviewed against doRender (internal/screenwriter, D624): review it, then pin the new numbers",
			pis.Frames, pis.Unread, screenWriterPisUnread, screenWriterPisFrames)
	}
}

// The frames of the pinned Pi's fixture (Pi 1.5.2, 500 scenarios) as the writer reads them: how many there are, and for how many it
// has no rule.
const (
	screenWriterPisFrames = 6338
	screenWriterPisUnread = 2831
)
