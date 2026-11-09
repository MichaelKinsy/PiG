package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode/tui"
)

// The tutorial's checks (docs/chord/tutorial.md), as Pi's tutorial.test.ts has them: every Go, TypeScript, JSON and CSS block of
// the book is quoted from this example's files (`<!-- quote: path -->` above it) and fails here when the file no longer has those
// lines; the example's steps run (`run`, the code the book quotes), and every console output the book shows
// (`<!-- output: main -->`, and `<!-- output: tui -->` with a Node for the terminal's sandbox) is among the lines they print, in order.

const bookPath = "../../docs/chord/tutorial.md"

// block is a fenced block of the book: its language, the marker comment right above it (if any), its text and line.
type block struct {
	lang   string
	marker string
	text   string
	line   int
}

var (
	fence  = regexp.MustCompile("^```(\\w*)$")
	marker = regexp.MustCompile(`^<!-- (.+) -->$`)
)

// blocksOf is every fenced block of book, with the `<!-- … -->` marker on the line before its opening fence.
func blocksOf(book string) []block {
	lines := strings.Split(book, "\n")
	var found []block
	for n := 0; n < len(lines); n++ {
		open := fence.FindStringSubmatch(lines[n])
		if open == nil {
			continue
		}
		end := n + 1
		for end < len(lines) && lines[end] != "```" {
			end++
		}
		var mark string
		if n > 0 {
			if m := marker.FindStringSubmatch(lines[n-1]); m != nil {
				mark = m[1]
			}
		}
		found = append(found, block{lang: open[1], marker: mark, text: strings.Join(lines[n+1:end], "\n"), line: n + 1})
		n = end
	}
	return found
}

func bookBlocks(t *testing.T) []block {
	t.Helper()
	book, err := os.ReadFile(bookPath)
	if err != nil {
		t.Fatal(err)
	}
	return blocksOf(string(book))
}

// marked is the blocks whose marker is `<kind>: <value>`, with the value.
func marked(blocks []block, kind string) (found []block, values []string) {
	for _, b := range blocks {
		if value, ok := strings.CutPrefix(b.marker, kind+": "); ok {
			found, values = append(found, b), append(values, value)
		}
	}
	return found, values
}

// expectPrinted fails unless every line of want is among printed's lines, in order.
func expectPrinted(t *testing.T, want block, printed string) {
	t.Helper()
	lines := strings.Split(printed, "\n")
	at := 0
	for line := range strings.SplitSeq(want.text, "\n") {
		found := -1
		for i := at; i < len(lines); i++ {
			if strings.TrimRight(lines[i], " \t") == strings.TrimRight(line, " \t") {
				found = i
				break
			}
		}
		if found < 0 {
			t.Errorf("tutorial.md:%d: not printed (in this order): %s", want.line, line)
			return
		}
		at = found + 1
	}
}

func TestTheTutorialQuotesEveryGoTypeScriptJSONAndCSSBlockFromAFile(t *testing.T) {
	var unquoted []string
	for _, b := range bookBlocks(t) {
		switch b.lang {
		case "go", "ts", "tsx", "json", "css":
			if !strings.HasPrefix(b.marker, "quote: ") {
				unquoted = append(unquoted, "tutorial.md:"+strconv.Itoa(b.line))
			}
		}
	}
	if len(unquoted) > 0 {
		t.Fatalf("blocks with no quote marker: %v", unquoted)
	}
}

func TestTheTutorialsBlocksMatchTheExamplesFiles(t *testing.T) {
	quotes, values := marked(bookBlocks(t), "quote")
	if len(quotes) < 40 {
		t.Fatalf("the book quotes %d blocks, want at least 40", len(quotes))
	}
	for i, b := range quotes {
		file, err := os.ReadFile(filepath.FromSlash(values[i]))
		if err != nil {
			t.Fatalf("tutorial.md:%d: %v", b.line, err)
		}
		if !strings.Contains(string(file), b.text) {
			t.Errorf("tutorial.md:%d: %s no longer has this block", b.line, values[i])
		}
	}
}

func TestTheTutorialsStepsPrintWhatTheBookShows(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), t.TempDir(), &out, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	outputs, values := marked(bookBlocks(t), "output")
	shown := 0
	// The terminal's output (`<!-- output: tui -->`) is checked when a Node for its sandbox is there, as Pi's test does.
	sandbox := tui.CheckSandboxNode(terminalNode()) == nil
	for i, b := range outputs {
		if values[i] == "main" || (values[i] == "tui" && sandbox) {
			expectPrinted(t, b, out.String())
			shown++
		}
	}
	if shown == 0 {
		t.Fatal("the book shows no output of main")
	}
}

func TestMain(m *testing.M) {
	if os.Getenv(workerEnv) != "" {
		startWorker()
		return
	}
	os.Exit(m.Run())
}
