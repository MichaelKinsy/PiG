// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package tui

import (
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

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
	// frame, when set, is told of every write that is a synchronized update by its own bytes: the bytes, the screen's columns, and
	// whether the writer counted it as a frame it has no rule for.
	frame func(update string, cols int, unread bool)
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
		before := r.writer.Stats().Unread
		translated.WriteString(r.writer.Translate(write))
		if r.frame != nil && strings.HasPrefix(write, "\x1b[?2026h") && strings.HasSuffix(write, "\x1b[?2026l") {
			r.frame(write, r.size[0], r.writer.Stats().Unread != before)
		}
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
		_ = file.Close()
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
// writer reads is the one Pi's doRender writes: of the frames of Pi's own 500 scenarios, the ones it has no rule for are exactly
// the ones screenWriterNoRule names from their bytes, frame by frame: an image, a line feed inside a line, a control sequence cut
// short (a line Pi sliced inside an SGR or a hyperlink, so the next escape comes before the sequence's end) and a screen narrower
// than two columns, and the frames of screenWriterKeptFrames, named by step and hash. So a frame Pi starts to write another way shows here, and so does a frame of those kinds the writer starts to
// read. The fixture decides how many there are of each: a re-recorded fixture changes no number here, and a failure prints them.
func TestScreenWriterShowsTheRenderersScreens(t *testing.T) {
	var streams, steps, differences int
	var total, pis screenwriter.Stats
	noRule := map[string]int{}
	var misread []string
	updates := 0
	keptSeen := make([]int, len(screenWriterKeptFrames))
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
				if name := replay.name; variant == "B" {
					replay.frame = func(update string, cols int, unread bool) {
						updates++
						why := screenWriterNoRule(update, cols)
						if why == "" && unread {
							if k := screenWriterKeptFrame(name, index, update); k >= 0 {
								why = screenWriterNoRules[len(screenWriterNoRules)-1]
								keptSeen[k]++
							}
						}
						if why != "" {
							noRule[why]++
						}
						if unread != (why != "") {
							misread = append(misread, fmt.Sprintf("%s step %d, %d columns: the writer had no rule: %v; the frame holds %q; sha256 %x: %q", name, index, cols, unread, why, sha256.Sum256([]byte(update)), update))
						}
					}
				}
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
	for i, frame := range misread {
		if i == 5 {
			t.Errorf("and %d more frames of Pi's fixture", len(misread)-i)
			break
		}
		t.Error(frame)
	}
	for k, kept := range screenWriterKeptFrames {
		if keptSeen[k] != 1 {
			t.Errorf("the frame of %s step %d (sha256 %s) was left as it came %d times, want once", kept.name, kept.step, kept.sha256, keptSeen[k])
		}
	}
	held := 0
	for _, why := range screenWriterNoRules {
		if noRule[why] == 0 {
			t.Errorf("no frame of Pi's fixture holds %s: nothing shows that the writer leaves such a frame as it came", why)
		}
		held += noRule[why]
	}
	if updates == 0 || pis.Frames != updates || pis.Unread != held || t.Failed() {
		t.Errorf("Pi's fixture has %d synchronized updates, and %d hold what the frame grammar (internal/screenwriter, D624) has no rule for: %v; the writer read %d frames and had no rule for %d",
			updates, held, noRule, pis.Frames, pis.Unread)
	}
}

// screenWriterNoRules are what a synchronized update of Pi's doRender can hold that the writer's frame grammar has no rule for.
var screenWriterNoRules = []string{"a screen narrower than two columns", "an image", "a line feed inside a line", "a control sequence cut short", "a row written under a pen the writer cannot make (screenWriterKeptFrames)"}

type screenWriterKept struct {
	name   string
	step   int
	sha256 string
}

// screenWriterKeptFrames are the frames of Pi's fixture, by scenario, step and the hash of their bytes, that the writer reads and then sends as it
// came although their bytes hold nothing screenWriterNoRule names: no more and no fewer. The recording made with Pi b7bb7ca905b8 added the first:
// step 4 of scenario 485 (a 100 by 30 screen) rewrites rows 5, 12 and 13. The line of row 5 ends under a foreground colour it never resets, so row
// 12 is erased and written under that pen, and the line of row 12 ends bold: Writer.row would have to make the terminal's pen the frame's, and the
// writer makes no SGR sequence but a reset (setPen), so it declines the row and the frame is written as the renderer wrote it: the screens match
// (0 differences) and the bytes the terminal gets are Pi's. The cursor address that the last row lacks is not the reason: internal/screenwriter's
// TestKeptFrameIsDeclinedForTheOpenPenOfItsRows replays the frame and shows that an address after the last row changes nothing and that the same
// rows with a reset ending each line are read.
var screenWriterKeptFrames = []screenWriterKept{
	{"oracle-485 B", 4, "6b0ddf18a28c01b852865e3f7917fba5dba9a8f30a7948e0370dcd268710a03d"},
}

// screenWriterKeptFrame is the index of the entry of screenWriterKeptFrames that names this frame, or -1.
func screenWriterKeptFrame(name string, step int, update string) int {
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(update)))
	return slices.IndexFunc(screenWriterKeptFrames, func(k screenWriterKept) bool {
		return k.name == name && k.step == step && k.sha256 == sum
	})
}

// A CSI or an OSC that the next escape ends before its final byte or its terminator: a line cut inside a sequence.
var screenWriterCutSequence = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*\x1b|\x1b\\][^\x07\x1b]*\x1b[^\\\\]")

// screenWriterNoRule names what a synchronized update holds that the frame grammar has no rule for, "" for nothing. It reads the
// update's bytes and the screen's width, not the writer: a Kitty or iTerm2 image sequence, a line feed (Pi's doRender writes none
// between rows, so one is inside a line), a sequence cut short, or a screen of fewer than two columns.
func screenWriterNoRule(update string, cols int) string {
	switch {
	case cols < 2:
		return screenWriterNoRules[0]
	case strings.Contains(update, "\x1b_G") || strings.Contains(update, "\x1b]1337;File="):
		return screenWriterNoRules[1]
	case strings.Contains(update, "\n"):
		return screenWriterNoRules[2]
	case screenWriterCutSequence.MatchString(update):
		return screenWriterNoRules[3]
	}
	return ""
}

// An image's rows under an overlay, in the frames the alternate screen itself writes for them: a transcript with an image three
// rows high, an overlay shown over the image's rows, changed while it is shown and closed again, and text below the image that
// changes between them; on a Kitty terminal and on iTerm2, with the rows that moved scrolled and with Pi's bytes. For a Kitty
// terminal every frame that changes a row the image covers deletes the placements and draws the image again; for iTerm2 only the
// frame that draws the image's own row holds it, and the overlay's rows are ordinary frames. The writer sends
// every write that holds an image or deletes one as it came and translates frames between them, and after every frame and after
// the settle write the screens show the same cells with and without it, on every width model: the cells an image covers among
// them, which gridterm keeps as a terminal does that stores an image in the cells it covers (there the erase of a row takes the
// image from the row's cells, so a row the frame erased and the writer wrote in place would show).
func TestScreenWriterImageRowsUnderAnOverlay(t *testing.T) {
	for _, protocol := range []ImageProtocol{ImageProtocolKitty, ImageProtocolITerm2} {
		for _, scrollRows := range []bool{true, false} {
			t.Run(fmt.Sprint(protocol, "/scroll=", scrollRows), func(t *testing.T) {
				t.Setenv("PIG_TUI_SCROLL_ROWS", map[bool]string{true: "", false: "0"}[scrollRows])
				t.Setenv("TERM", "xterm-256color")
				SetCapabilities(TerminalCapabilities{Images: protocol, TrueColor: true})
				t.Cleanup(ResetCapabilitiesCache)
				const cols, rows = 40, 12
				text := &termLines{lines: []string{"top", EncodeKitty("AAAA", 4, 3, 11586, false), "", "", "after"}}
				if protocol == ImageProtocolITerm2 {
					text.lines = []string{"top", "", "", "\x1b[2A" + EncodeITerm2("AAAA", ITerm2Options{Width: 4, Height: 3}), "after"}
				}
				var out strings.Builder
				ui := NewTuiAltScreenWithOutput(&out, cols, rows, TuiAltScreenOptions{})
				ui.afterFunc = func(time.Duration, func()) stoppableTimer { return stoppedOracleTimer{} }
				ui.Add(text)
				ui.Start()
				t.Cleanup(ui.Stop)
				replay := newScreenWriterReplay(t.Name(), cols, rows)
				images := 0
				// frame draws a frame, gives its writes to the writer and compares the screens, then once more after the settle write.
				frame := func(what string) {
					t.Helper()
					ui.Render()
					written := out.String()
					out.Reset()
					var sent strings.Builder
					for _, write := range gridterm.SplitWrites(written) {
						translated := replay.writer.Translate(write)
						if IsImageLine(write) {
							images++
							if translated != write {
								t.Errorf("%s: a write that holds an image was not sent as it came\n  renderer wrote %q\n  writer wrote   %q", what, write, translated)
							}
						}
						sent.WriteString(translated)
					}
					if d := replay.pair.Step(written, sent.String()); d != "" {
						t.Errorf("%s: %s\n  renderer wrote %q\n  writer wrote   %q", what, d, written, sent.String())
					}
					if d := replay.pair.Step("", replay.writer.Settled()); d != "" {
						t.Errorf("%s: the settle write changed the screen: %s", what, d)
					}
				}
				last := func() *string { return &text.lines[len(text.lines)-1] }
				frame("the image")
				text.lines = append(text.lines, "", "a line below the image")
				frame("a line below the image")
				*last() += " grows"
				frame("the line below grows")
				overlay := &termLines{lines: []string{"+----------+", "| PANEL    |", "+----------+"}}
				handle := ui.ShowOverlay(overlay, OverlaySpec{Row: &OverlayValue{Value: 1}, Col: &OverlayValue{Value: 1}, Width: &OverlayValue{Value: 12}}.Options())
				frame("an overlay over the image's rows")
				overlay.lines[1] = "| PANEL!   |"
				frame("the overlay changes")
				*last() += " and grows"
				frame("the line below grows under the overlay")
				*last() += " again"
				frame("the line below grows under the overlay again")
				handle.Hide()
				frame("the overlay is closed")
				for _, after := range []string{"after!", "after!!", "after!!!"} {
					text.lines[4] = after
					frame("the row below the image changes to " + after)
				}
				stats := replay.writer.Stats()
				t.Logf("%d writes held an image; the writer's frames: %+v; what the screens have no rule for: %v", images, stats, replay.pair.Reference().Unknown())
				if images < 1 || stats.Translated < 4 {
					t.Errorf("%d writes held an image and the writer translated %d frames: the frames do not exercise an image under an overlay", images, stats.Translated)
				}
			})
		}
	}
}
