package codingagent

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/screenwriter"
	"github.com/MichaelKinsy/PiG/tui"
)

// screenWriterTestTerminal is a process terminal that writes to a file and takes no input: built before the interactive mode, as the
// process's terminal is, so the setting has to reach a terminal that exists.
type screenWriterTestTerminal struct{ *tui.ProcessTerminal }

func (screenWriterTestTerminal) Start(func(string), func()) error              { return nil }
func (screenWriterTestTerminal) Stop()                                         {}
func (screenWriterTestTerminal) DrainInput(time.Duration, time.Duration) error { return nil }
func (screenWriterTestTerminal) RestoreOnExit(string, string)                  {}

// screenWriterRun is one interactive mode on a screenWriterTestTerminal. Kept are what the terminal was sent (sent), the bytes the
// renderers wrote (PI_TUI_WRITE_LOG, above the screen writer) and the screen writer's records (PIG_TUI_SCREEN_WRITER_LOG, one per
// write that passed through a writer).
type screenWriterRun struct {
	t        *testing.T
	mode     *InteractiveMode
	sent     string
	written  string
	records  string
	agentDir string
	manager  *SettingsManager
}

// startScreenWriterRun starts interactive mode in tuiMode with the global settings file holding terminal ("" for no terminal object)
// and the environment variable set to env. prepare runs before the first renderer is mounted.
func startScreenWriterRun(t *testing.T, env, terminal, tuiMode string, prepare func(m *InteractiveMode)) *screenWriterRun {
	t.Helper()
	// The choice is the process's, and a terminal gets its screen writer when it is built: the terminal of this run is built with
	// one, whatever the default is, and the start of interactive mode then keeps or drops it as the setting says. The tests after
	// this one find the process as this one found it at this line.
	t.Cleanup(func() { tui.SetScreenWriter(true) })
	tui.SetScreenWriter(true)
	dir := t.TempDir()
	r := &screenWriterRun{t: t, sent: filepath.Join(dir, "sent"), written: filepath.Join(dir, "written"), records: filepath.Join(dir, "records"), agentDir: t.TempDir()}
	t.Setenv("PIG_TUI_SCREEN_WRITER", env)
	t.Setenv("PI_TUI_WRITE_LOG", r.written)
	t.Setenv("PIG_TUI_SCREEN_WRITER_LOG", r.records)
	file, err := os.Create(r.sent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	processTerminal := screenWriterTestTerminal{tui.NewProcessTerminalWithOutput(nil, nil, file)}
	settings := `{"tuiMode":"` + tuiMode + `"}`
	if terminal != "" {
		settings = `{"tuiMode":"` + tuiMode + `","terminal":` + terminal + `}`
	}
	writeSettingsFixture(t, filepath.Join(r.agentDir, "settings.json"), settings)
	cwd := t.TempDir()
	r.manager = NewSettingsManager(cwd, r.agentDir)
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	r.mode = newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{
		CWD: cwd, Model: model, AgentDir: r.agentDir, Settings: r.manager.Get(), SettingsManager: r.manager, Terminal: processTerminal,
	}, nil)
	if prepare != nil {
		prepare(r.mode)
	}
	r.mode.mountInteractiveTui(true)
	t.Cleanup(func() {
		r.mode.teardownCurrentTui()
		if r.mode.altScreen != nil {
			r.mode.altScreen.StopWithOptions(tui.StopOptions{PreserveScreen: true})
		}
	})
	return r
}

// frames draws a transcript that grows by a word at a time, and one frame that changes nothing: frames of which the screen writer
// sends a part, or nothing.
func (r *screenWriterRun) frames(label string) {
	text := tui.NewText(label + " one")
	r.mode.chatContainer.Add(text)
	r.mode.tuiInst.Render()
	for _, more := range []string{" two", " three", " three"} {
		text.SetText(label + " one" + more)
		r.mode.tuiInst.Render()
	}
}

func (r *screenWriterRun) switchTo(mode string) {
	r.t.Helper()
	if !r.mode.switchTuiMode(mode, false, true) {
		r.t.Fatalf("the mode did not switch to %s", mode)
	}
}

// screenWriterFrames are the synchronized updates in data, in order: the frames. Every frame is a Write of a renderer, so
// PI_TUI_WRITE_LOG holds them all; the terminal's cursor, start and stop bytes are not logged there and are no frames.
func screenWriterFrames(data string) []string {
	const begin, end = "\x1b[?2026h", "\x1b[?2026l"
	var frames []string
	for {
		start := strings.Index(data, begin)
		if start < 0 {
			return frames
		}
		stop := strings.Index(data[start:], end)
		if stop < 0 {
			return frames
		}
		frames = append(frames, data[start:start+stop+len(end)])
		data = data[start+stop+len(end):]
	}
}

// check requires that every frame, from the first one, went the way writer says. Through a screen writer: the frames the writers
// read are the frames the renderers wrote, the terminal got exactly what the writers sent, and frames were translated. As written:
// no write was recorded by a screen writer and the frames the terminal got are the frames the renderers wrote. The alternate screen
// must have been entered, so frames were at stake.
func (r *screenWriterRun) check(writer bool) {
	r.t.Helper()
	writtenBytes, _ := os.ReadFile(r.written)
	log, _ := os.ReadFile(r.records)
	sentBytes, _ := os.ReadFile(r.sent)
	sent, wrote := string(sentBytes), screenWriterFrames(string(writtenBytes))
	if !strings.Contains(sent, "\x1b[?1049h") || len(wrote) == 0 {
		r.t.Fatalf("no alternate-screen frame: the terminal got %d bytes, the renderers wrote %d frames", len(sent), len(wrote))
	}
	if !writer {
		if len(log) != 0 {
			r.t.Errorf("%d bytes of screen writer records in byte mode: a write passed through a screen writer", len(log))
		}
		if got := screenWriterFrames(sent); !slices.Equal(got, wrote) {
			r.t.Errorf("in byte mode the terminal got %d frames, the renderers wrote %d: they differ", len(got), len(wrote))
		}
		return
	}
	var in, out strings.Builder
	translated := 0
	for rest := log; len(rest) > 0; {
		kind, recordIn, recordOut, next, ok := screenwriter.NextRecord(rest)
		if !ok {
			r.t.Fatalf("the screen writer log does not parse at byte %d of %d", len(log)-len(rest), len(log))
		}
		in.Write(recordIn)
		out.Write(recordOut)
		if kind == screenwriter.RecordFrame && !bytes.Equal(recordIn, recordOut) {
			translated++
		}
		rest = next
	}
	if got := screenWriterFrames(in.String()); !slices.Equal(got, wrote) {
		r.t.Errorf("the screen writers read %d frames, the renderers wrote %d: a frame went past the screen writer", len(got), len(wrote))
	}
	if out.String() != sent {
		r.t.Errorf("the screen writers sent %d bytes, the terminal got %d", out.Len(), len(sent))
	}
	if translated == 0 {
		r.t.Errorf("no frame was translated: the test's frames do not tell the two modes apart")
	}
}

// The choice between the screen writer and Pi's bytes is made before the first byte of the first alternate-screen frame on every
// way into the alternate screen, and holds for every frame after it: the environment decides when it is set, else the setting, and
// an invalid or missing setting is the default.
func TestScreenWriterChoiceIsMadeBeforeTheFirstFrame(t *testing.T) {
	choices := []struct {
		name, env, terminal string
		writer              bool
	}{
		{"setting writer", "", `{"screenWriter":"writer"}`, true},
		{"setting pi", "", `{"screenWriter":"pi"}`, false},
		{"no setting", "", ``, screenWriterByDefault},
		{"invalid setting", "", `{"screenWriter":"fast"}`, screenWriterByDefault},
		{"environment pi over setting writer", "pi", `{"screenWriter":"writer"}`, false},
		{"environment 0 over setting writer", "0", `{"screenWriter":"writer"}`, false},
		{"environment 1 over setting pi", "1", `{"screenWriter":"pi"}`, true},
		{"unknown environment value over setting pi", "on", `{"screenWriter":"pi"}`, true},
	}
	paths := []struct {
		name string
		run  func(t *testing.T, env, terminal string) *screenWriterRun
	}{
		{"fresh start in fullscreen", func(t *testing.T, env, terminal string) *screenWriterRun {
			r := startScreenWriterRun(t, env, terminal, "fullscreen", nil)
			r.frames("fresh")
			return r
		}},
		{"start with a restored transcript (resume, continue)", func(t *testing.T, env, terminal string) *screenWriterRun {
			r := startScreenWriterRun(t, env, terminal, "fullscreen", func(m *InteractiveMode) {
				for _, line := range []string{"user: earlier question", "assistant: earlier answer"} {
					m.chatContainer.Add(tui.NewText(line))
				}
			})
			r.frames("resumed")
			return r
		}},
		{"fullscreen entered after a regular start", func(t *testing.T, env, terminal string) *screenWriterRun {
			r := startScreenWriterRun(t, env, terminal, "regular", nil)
			r.frames("regular")
			r.switchTo("fullscreen")
			r.frames("fullscreen")
			return r
		}},
		{"mode switched at run time", func(t *testing.T, env, terminal string) *screenWriterRun {
			r := startScreenWriterRun(t, env, terminal, "fullscreen", nil)
			r.frames("first")
			r.switchTo("regular")
			r.frames("regular")
			r.switchTo("fullscreen")
			r.frames("second")
			return r
		}},
	}
	for _, path := range paths {
		for _, choice := range choices {
			t.Run(path.name+"/"+choice.name, func(t *testing.T) {
				path.run(t, choice.env, choice.terminal).check(choice.writer)
			})
		}
	}
}

// The setting is read once, at the start. Changed under a running session and then refreshed by the two handlers that refresh
// the session's settings, /reload and a change applied in /settings, it leaves the frames of the live screen, and of a renderer
// mounted afterwards, in the mode the session started in. Both handlers are the real ones (buildSlashContext).
func TestScreenWriterSettingChangeWaitsForTheNextStart(t *testing.T) {
	for _, tc := range []struct{ start, change string }{{"writer", "pi"}, {"pi", "writer"}} {
		for _, refresh := range []string{"reload", "settings"} {
			t.Run(tc.start+" to "+tc.change+"/"+refresh, func(t *testing.T) {
				restoreStartupTheme(t)
				r := startScreenWriterRun(t, "", `{"screenWriter":"`+tc.start+`"}`, "fullscreen", nil)
				t.Cleanup(func() {
					r.mode.stopInteractiveTui()
					r.mode.backgroundTasks.Wait()
				})
				r.frames("before")
				if refresh == "reload" {
					writeSettingsFixture(t, filepath.Join(r.agentDir, "settings.json"), `{"tuiMode":"fullscreen","terminal":{"screenWriter":"`+tc.change+`"}}`)
					if err := r.mode.buildSlashContext(t.Context()).Reload(); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := r.manager.UpdateGlobal(func(s *Settings) { s.TerminalScreenWriter = tc.change }); err != nil {
						t.Fatal(err)
					}
					r.mode.buildSlashContext(t.Context()).OnSettingApplied("clear-on-shrink", "true")
				}
				if got := r.mode.opts.Settings.GetTerminalScreenWriter(); got != tc.change {
					t.Fatalf("after the %s refresh the session's setting is %q, want %q: the refresh did not happen", refresh, got, tc.change)
				}
				r.frames("after the refresh")
				r.switchTo("regular")
				r.switchTo("fullscreen")
				r.frames("after a switch")
				r.check(tc.start == "writer")
			})
		}
	}
}

// screenWriterByDefault is whether the default of terminal.screenWriter is the screen writer. The tests of this file say "the
// default" through it and through TerminalScreenWriterDefault, so they hold for either value of that one constant.
const screenWriterByDefault = TerminalScreenWriterDefault == TerminalScreenWriterWriter

// A value of terminal.screenWriter that is neither "writer" nor "pi", and the key left out, mean the default, whichever of the two
// the default is; the two values mean themselves under either default.
func TestTerminalScreenWriterUnderEitherDefault(t *testing.T) {
	for _, byDefault := range []string{TerminalScreenWriterWriter, TerminalScreenWriterPi} {
		for value, want := range map[string]string{"writer": TerminalScreenWriterWriter, "pi": TerminalScreenWriterPi, "": byDefault, "Writer": byDefault, "PI": byDefault, "0": byDefault, "1": byDefault, "fast": byDefault} {
			if got := terminalScreenWriterOr(value, byDefault); got != want {
				t.Errorf("with the default %q, terminal.screenWriter %q reads as %q, want %q", byDefault, value, got, want)
			}
		}
	}
	if terminalScreenWriterOr(TerminalScreenWriterDefault, "") == "" {
		t.Errorf("the default %q is neither value of terminal.screenWriter", TerminalScreenWriterDefault)
	}
	if got := (Settings{}).GetTerminalScreenWriter(); got != TerminalScreenWriterDefault {
		t.Errorf("with the key left out the setting reads %q, want the default %q", got, TerminalScreenWriterDefault)
	}
}

// terminal.screenWriter is the default unless it names the other value, is read from the global file only, and a rewrite of the
// settings file keeps it.
func TestTerminalScreenWriterSetting(t *testing.T) {
	byDefault, other := TerminalScreenWriterDefault, TerminalScreenWriterPi
	if byDefault == TerminalScreenWriterPi {
		other = TerminalScreenWriterWriter
	}
	agentDir, cwd := t.TempDir(), t.TempDir()
	if got := NewSettingsManager(cwd, agentDir).GetTerminalScreenWriter(); got != byDefault {
		t.Fatalf("with no settings file = %q, want the default %q", got, byDefault)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSettingsFixture(t, filepath.Join(cwd, ".pig", "settings.json"), `{"terminal":{"screenWriter":"`+other+`","showTerminalProgress":true}}`)
	project := NewSettingsManager(cwd, agentDir)
	if got, merged := project.GetTerminalScreenWriter(), project.Get().GetTerminalScreenWriter(); got != byDefault || merged != byDefault {
		t.Fatalf("a project file selected %q (merged view %q)", got, merged)
	}
	if !project.GetShowTerminalProgress() {
		t.Fatalf("the project file's other terminal keys were not read")
	}
	settingsPath := filepath.Join(agentDir, "settings.json")
	for value, want := range map[string]string{"writer": TerminalScreenWriterWriter, "pi": TerminalScreenWriterPi, "": byDefault, "Writer": byDefault, "0": byDefault, "fast": byDefault} {
		writeSettingsFixture(t, settingsPath, `{"terminal":{"screenWriter":"`+value+`"}}`)
		sm := NewSettingsManager(cwd, agentDir)
		if got, merged := sm.GetTerminalScreenWriter(), sm.Get().GetTerminalScreenWriter(); got != want || merged != want {
			t.Errorf("terminal.screenWriter %q reads as %q (merged view %q), want %q", value, got, merged, want)
		}
	}
	writeSettingsFixture(t, settingsPath, `{"terminal":{"screenWriter":"`+other+`","clearOnShrink":true}}`)
	sm := NewSettingsManager(cwd, agentDir)
	if err := sm.UpdateGlobal(func(s *Settings) { s.Theme = "light" }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"screenWriter": "`+other+`"`) && !strings.Contains(string(data), `"screenWriter":"`+other+`"`) {
		t.Fatalf("the rewritten settings file lost terminal.screenWriter: %s", data)
	}
	if got := NewSettingsManager(cwd, agentDir).GetTerminalScreenWriter(); got != other {
		t.Fatalf("after the rewrite the setting reads %q, want %q", got, other)
	}
}
