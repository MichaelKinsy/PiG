package codingagent

// pi: packages/coding-agent/src/modes/interactive/bug-report.ts

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// slotText is the text of the editor slot's component at width 120, read on the owner loop.
func (r *promptRig) slotText(t *testing.T) string {
	t.Helper()
	return onOwner(t, r, func() string {
		children := r.m.editorContainer.Children()
		if len(children) != 1 {
			return ""
		}
		return widthx.StripAnsi(strings.Join(children[0].Render(120), "\n"))
	})
}

// bugRun drives the real /bug handler of the rig's InteractiveMode on the owner goroutine and records the statuses it shows.
type bugRun struct {
	done     <-chan error
	statuses *[]string
}

func startBugReport(r *promptRig) bugRun {
	statuses := new([]string)
	done := ownerCall(r, func() error {
		sc := r.m.buildSlashContext(r.m.runCtx)
		sc.ShowStatus = func(text string) { *statuses = append(*statuses, text) }
		return bugHandler(sc)
	})
	return bugRun{done: done, statuses: statuses}
}

// answerBugPrompts walks the prompts the way packages/coding-agent/test/bug-report.test.ts "preserves line breaks in pasted
// descriptions" does: a bracketed paste with CRLF line breaks and an indented line into the description editor, then
// "No" to the transcript, "No" to the summary, and the delivery summary the description appears in.
func answerBugPrompts(t *testing.T, r *promptRig) string {
	t.Helper()
	r.untilSlotShows(t, "Report a bug")
	r.press(t, "\x1b[200~Request failed\r\n  ↳ pi exiting...\r\nstack trace\x1b[201~")
	r.press(t, "\r")
	r.untilSlotShows(t, "Include the session transcript?")
	if !strings.Contains(r.slotText(t), "Include the session transcript?") {
		t.Fatalf("transcript prompt = %q", r.slotText(t))
	}
	r.press(t, "j")
	r.press(t, "\r")
	r.untilSlotShows(t, "Attach a summary written by")
	r.press(t, "j")
	r.press(t, "\r")
	r.untilSlotShows(t, "Description: Request failed")
	return r.slotText(t)
}

func TestBugReportPromptPreservesLineBreaksInPastedDescriptions(t *testing.T) {
	r := newPromptRig(t)
	run := startBugReport(r)
	delivery := answerBugPrompts(t, r)
	lines := strings.Split(delivery, "\n")
	for _, want := range []string{"Description: Request failed", "↳ pi exiting...", "stack trace"} {
		if !slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, want) }) {
			t.Errorf("delivery prompt lacks %q:\n%s", want, delivery)
		}
	}
	// The delivery prompt offers the export and Cancel; Cancel ends the report with the cancelled status.
	r.press(t, "j")
	r.press(t, "\r")
	if err := receive(t, run.done, "/bug"); err != nil {
		t.Fatal(err)
	}
	if got := onOwner(t, r, func() []string { return slices.Clone(*run.statuses) }); !slices.Equal(got, []string{"Bug report cancelled"}) {
		t.Fatalf("statuses = %q, want the cancelled status", got)
	}
	r.untilEditorShown(t)
}

// Exporting from the same prompts writes the archive whose metadata the interactive mode's state fills (bug-report.ts
// collectBugReportMetadata over the session, model, thinking level and settings the mode holds).
func TestBugReportExportCarriesTheInteractiveModeState(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	r := newPromptRig(t)
	onOwner(t, r, func() struct{} { r.m.thinkingLevel = "high"; return struct{}{} })
	run := startBugReport(r)
	answerBugPrompts(t, r)
	r.press(t, "\r") // the first delivery option exports
	if err := receive(t, run.done, "/bug"); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "pig-bug-report-*.zip"))
	if len(matches) != 1 {
		t.Fatalf("archives = %v", matches)
	}
	var report BugReportMetadata
	if err := json.Unmarshal(readZip(t, matches[0])["report.json"], &report); err != nil {
		t.Fatal(err)
	}
	if report.Hint == nil || *report.Hint != "Request failed\n  ↳ pi exiting...\nstack trace" {
		t.Errorf("hint = %v, want the pasted description with its line breaks", report.Hint)
	}
	if report.ThinkingLevel != "high" || report.Session.Included || report.Session.SummaryIncluded {
		t.Errorf("report = %+v, want thinking level high and neither transcript nor summary", report)
	}
	if report.Environment.Version != InstalledPigVersion {
		t.Errorf("version = %q, want the installed version %q", report.Environment.Version, InstalledPigVersion)
	}
}
