package activity

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
)

// Kind is the kind of work a step is, for its icon.
type Kind string

// The kinds.
const (
	KindCommand Kind = "command"
	KindSearch  Kind = "search"
	KindRead    Kind = "read"
	KindEdit    Kind = "edit"
	KindTool    Kind = "tool"
)

// Style says what a label's object is: a file (bold), a command (code font) or the agent's words.
type Style string

// The styles.
const (
	StyleNone Style = ""
	StyleFile Style = "file"
	StyleCode Style = "code"
)

// Label is a step's words: a verb, then what it acted on, then a quiet extra such as a count.
type Label struct {
	Kind         Kind
	Verb, Object string
	Style        Style
	Extra        string
}

// Described is a step for the sheet: its label in the past tense and, if there is more to say, a description.
type Described struct {
	Label       Label
	Description string
	Mono        bool
}

var search = regexp.MustCompile(`^\s*(rg|grep|egrep|fgrep|find|fd|ag|ack)\b`)

func str(tool transcript.ToolView, key string) string {
	if tool.Args == nil {
		return ""
	}
	s, _ := tool.Args.Lookup(key).(string)
	return s
}

func firstLine(text string) string {
	for _, l := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

func lastLine(text string) string {
	l := strings.Split(strings.TrimRight(text, " \t\r\n"), "\n")
	return strings.TrimSpace(l[len(l)-1])
}

func fileName(path string) string {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' })
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}

func failure(s Step) string {
	if s.Status != StepError {
		return ""
	}
	return firstLine(s.Tool.Output)
}

func shortArgument(tool transcript.ToolView) string {
	if tool.Args == nil {
		return ""
	}
	for _, k := range tool.Args.Keys {
		if v, ok := tool.Args.Lookup(k).(string); ok && strings.TrimSpace(v) != "" {
			l := firstLine(v)
			if len(l) > 120 {
				l = l[:120]
			}
			return l
		}
	}
	return ""
}

// Describe is a step in the past tense, by its tool's own words (bash, read, write and edit, as the coding-tools plugin's
// activity contribution has them) or the tool's name and first short argument.
func Describe(s Step) Described {
	switch s.Tool.Name {
	case "bash":
		cmd := str(s.Tool, "command")
		k, verb := KindCommand, "Ran"
		if search.MatchString(cmd) {
			k, verb = KindSearch, "Searched"
		}
		d := failure(s)
		if d == "" {
			d = lastLine(s.Tool.Output)
		}
		return Described{Label{Kind: k, Verb: verb, Object: cmd, Style: StyleCode}, d, true}
	case "read":
		path := str(s.Tool, "path")
		d := failure(s)
		if d == "" {
			switch {
			case len(s.Tool.Images) > 0:
				d = "Image"
			default:
				if lo, hi, ok := readRange(s.Tool); ok {
					d = fmt.Sprintf("Lines %d–%d", lo, hi)
				} else {
					d = path
				}
			}
		}
		return Described{Label{Kind: KindRead, Verb: "Read", Object: fileName(path), Style: StyleFile}, d, false}
	case "write", "edit":
		verb := "Wrote"
		if s.Tool.Name == "edit" {
			verb = "Edited"
		}
		path := str(s.Tool, "path")
		d := failure(s)
		if d == "" {
			d = path
		}
		return Described{Label{Kind: KindEdit, Verb: verb, Object: fileName(path), Style: StyleFile}, d, false}
	}
	l := Label{Kind: KindTool, Verb: s.Tool.Name}
	if o := shortArgument(s.Tool); o != "" {
		l.Object, l.Style = o, StyleCode
	}
	return Described{Label: l, Description: failure(s), Mono: true}
}

func readRange(tool transcript.ToolView) (lo, hi int, ok bool) {
	if tool.Args == nil {
		return 0, 0, false
	}
	off, isNum := tool.Args.Lookup("offset").(float64)
	if !isNum {
		return 0, 0, false
	}
	n := 1
	if l, ok := tool.Args.Lookup("limit").(float64); ok {
		n = int(l)
	}
	return int(off), int(off) + n - 1, true
}

// Live is a step's label in the present tense while it runs.
func Live(s Step) Label {
	if s.Status != StepRunning {
		return Describe(s).Label
	}
	switch s.Tool.Name {
	case "bash":
		cmd := firstLine(str(s.Tool, "command"))
		if search.MatchString(cmd) {
			return Label{Kind: KindSearch, Verb: "Searching", Object: cmd, Style: StyleCode}
		}
		return Label{Kind: KindCommand, Verb: "Running", Object: cmd, Style: StyleCode}
	case "read":
		return Label{Kind: KindRead, Verb: "Reading", Object: fileName(str(s.Tool, "path")), Style: StyleFile}
	case "write":
		return Label{Kind: KindEdit, Verb: "Writing", Object: fileName(str(s.Tool, "path")), Style: StyleFile}
	case "edit":
		return Label{Kind: KindEdit, Verb: "Editing", Object: fileName(str(s.Tool, "path")), Style: StyleFile}
	}
	return Describe(s).Label
}

// Plain is a label's words without styling.
func (l Label) Plain() string {
	var w []string
	for _, p := range []string{l.Verb, l.Object, l.Extra} {
		if p != "" {
			w = append(w, p)
		}
	}
	return strings.Join(w, " ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf(many, n)
}

// summaryWords is what a finished run's summary says for count calls of a tool; the key counts tools that share their words together
// (write and edit: "edited 2 files").
func summaryWords(name string) (key string, words func(int) string) {
	switch name {
	case "bash":
		return "bash", func(n int) string { return plural(n, "ran a command", "ran %d commands") }
	case "read":
		return "read", func(n int) string { return plural(n, "read a file", "read %d files") }
	case "write", "edit":
		return "edited", func(n int) string { return plural(n, "edited a file", "edited %d files") }
	}
	return "tool:" + name, func(n int) string { return plural(n, "used "+name, "used "+name+" %d times") }
}

// Summary is one line for a finished run of calls: the tools' own words for what they did, counted, in the order they did it
// ("Read 3 files, ran 2 commands"); failures adds " · 1 failed".
func Summary(steps []Step, failures bool) string {
	type count struct {
		words func(int) string
		n     int
	}
	var order []string
	counts := map[string]*count{}
	for _, s := range steps {
		key, words := summaryWords(s.Tool.Name)
		c := counts[key]
		if c == nil {
			c = &count{words: words}
			counts[key] = c
			order = append(order, key)
		}
		c.n++
	}
	var parts []string
	for _, k := range order {
		parts = append(parts, counts[k].words(counts[k].n))
	}
	text := strings.Join(parts, ", ")
	if text == "" {
		text = "done"
	}
	r := []rune(text)
	r[0] = unicode.ToUpper(r[0])
	out := string(r)
	if failures {
		errs := 0
		for _, s := range steps {
			if s.Status == StepError {
				errs++
			}
		}
		if errs > 0 {
			out += fmt.Sprintf(" · %d failed", errs)
		}
	}
	return out
}

// Done is the words of a finished activity row: "Thought, read 3 files" with thinking on it, else the calls' summary.
func (a Activity) Done() string {
	thought := false
	for _, e := range a.Entries {
		thought = thought || e.Kind == EntryThought
	}
	if thought {
		calls := ""
		if len(a.Steps) > 0 {
			calls = Summary(a.Steps, false)
			calls = strings.ToLower(calls[:1]) + calls[1:]
		}
		if calls == "" {
			return "Thought"
		}
		return "Thought, " + calls
	}
	if len(a.Steps) == 0 {
		return Summary(nil, true)
	}
	return Summary(a.Steps, true)
}

// Face is the words an activity row shows now: the step or thought the agent is at while it works, the summary and the count of steps once done.
func (a Activity) Face() (words, extra string, label *Label, failed bool) {
	if a.Working {
		if n := len(a.Entries); n > 0 {
			e := a.Entries[n-1]
			if e.Kind == EntryThought {
				if e.Live {
					return "Thinking…", "", nil, false
				}
				return "Thought", "", nil, false
			}
			l := Live(*e.Step)
			return l.Plain(), "", &l, e.Step.Status == StepError
		}
		return "Working…", "", nil, false
	}
	steps := len(a.Entries)
	return a.Done(), fmt.Sprintf(" · %d %s", steps, map[bool]string{true: "step", false: "steps"}[steps == 1]), nil, false
}
