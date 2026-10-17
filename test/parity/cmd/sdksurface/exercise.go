package main

import (
	"bytes"
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	consistencyPath = "docs/extension-sdk-consistency.md"
	gapsPath        = "test/parity/sdk-conformance-gaps.toml"
)

// conformanceCorpus is, per runtime, the extension code the conformance suite
// loads into the real host: the fixtures and scenario drivers under
// test/extension-conformance, and for the non-Go runtimes the extension sources
// that conformance tests embed as raw string literals in their Go test files.
// Only those literals count: the Go code, comments and other strings of a test
// file name Go types and host actions, not the runtime's API. A Go file is part
// of the Go corpus only when it imports the Go SDK: otherwise it calls the Go
// reference API or describes a scenario, not the SDK. A surface an SDK's
// fixtures never reference is not exercised through that SDK by any
// conformance test, whatever the matrix says about its presence.
var conformanceCorpus = map[string]struct {
	dirs []string
	exts []string
}{
	"node":   {dirs: []string{"test/extension-conformance"}, exts: []string{".mjs", ".js", ".ts", "_test.go"}},
	"go":     {dirs: []string{"test/extension-conformance"}, exts: []string{".go"}},
	"rust":   {dirs: []string{"test/extension-conformance"}, exts: []string{".rs", "_test.go"}},
	"python": {dirs: []string{"test/extension-conformance"}, exts: []string{".py", "_test.go"}},
}

// gapSpec is one reviewed conformance hole: a surface an SDK implements that no
// conformance fixture of that SDK reaches.
type gapSpec struct {
	Key  string   `toml:"key"`
	SDKs []string `toml:"sdks"`
	// Owner is the lane or person who closes the hole.
	Owner string `toml:"owner"`
	// Row names the conformance row, test or scenario that goes red or green when the hole closes.
	Row    string `toml:"row"`
	Reason string `toml:"reason"`
}

type gapsFile struct {
	Gaps []gapSpec `toml:"gap"`
}

// loadCorpus reads every fixture source of the runtime into one string.
func loadCorpus(root, sdk string) (string, error) {
	spec := conformanceCorpus[sdk]
	var b strings.Builder
	for _, dir := range spec.dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", "target", "__pycache__":
					return filepath.SkipDir
				}
				return nil
			}
			ok := false
			for _, ext := range spec.exts {
				ok = ok || strings.HasSuffix(path, ext)
			}
			if !ok {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			switch {
			case sdk == "go" && !bytes.Contains(data, []byte(`PiG/extensions/sdk"`)):
				return nil
			case sdk != "go" && strings.HasSuffix(path, ".go"):
				data = rawStringLiterals(data)
			}
			b.Write(data)
			b.WriteByte('\n')
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// rawStringLiterals returns the contents of the Go source's raw string
// literals, one per line: the extension sources a Go test embeds.
func rawStringLiterals(src []byte) []byte {
	var out bytes.Buffer
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, 0)
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return out.Bytes()
		}
		if tok == token.STRING && strings.HasPrefix(lit, "`") {
			out.WriteString(lit[1 : len(lit)-1])
			out.WriteByte('\n')
		}
	}
}

var identPart = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*$`)

// genericNames are member names common enough on unrelated objects (an
// AbortController's abort, an array's find) that a bare member access does not
// show the extension API was called. They count only behind a receiver.
var genericNames = map[string]bool{
	"abort": true, "shutdown": true, "compact": true, "on": true, "off": true, "emit": true, "find": true,
	"get": true, "set": true, "bold": true, "italic": true, "underline": true, "inverse": true,
	"strikethrough": true, "fg": true, "bg": true, "style": true, "colors": true, "theme": true,
	"exec": true, "reload": true, "fork": true, "select": true, "confirm": true, "input": true,
	"notify": true, "classify": true, "events": true,
}

// receiver is what may precede a generic member access for it to count: the
// extension API object, a context, one of its facades, a type path, or a call's
// result (`ctx.session_manager().get_header()`).
const receiver = `(?:\b(?:pi|api|ctx|context|ui|theme|events|bus|ext|extension|self|registry|manager|sm|mr|sessionManager|session_manager|modelRegistry|model_registry|\w*Context|\w*Registry|\w*Manager|\w*Theme|\w*Bus|\w*UI|\w*Extension)|\))`

// exerciseTokens returns the patterns that count as a conformance fixture
// referencing the cell's realization: a member access of its final identifier
// (`.setTitle`, `::set_title`) or, for an event, its constant or quoted name.
func exerciseTokens(r matrixRow, c cell) []*regexp.Regexp {
	var out []*regexp.Regexp
	for alt := range strings.SplitSeq(c.Symbol, "|") {
		alt = strings.TrimSpace(alt)
		isRead := strings.HasPrefix(alt, "read:")
		alt = strings.TrimPrefix(alt, "read:")
		if alt == "" {
			continue
		}
		if isRead && !genericNames[identPart.FindString(alt)] {
			// A property the runtime reads is handed over in an object literal, so the key names it as much as a member access does.
			out = append(out, regexp.MustCompile(`[{,]\s*`+regexp.QuoteMeta(identPart.FindString(alt))+`\s*:`))
		}
		name := identPart.FindString(alt)
		if name == "" {
			continue
		}
		if r.Implemented {
			// The host calls a member the extension defines: Go `func (p *T) Name(`, Rust `fn name(`, Python `def name(`.
			out = append(out, regexp.MustCompile(`(?:\bfunc\s*\([^)]*\)\s*|\bfn\s+|\bdef\s+)`+regexp.QuoteMeta(name)+`\s*\(`))
		}
		switch {
		case r.Kind == kindEvent && name != "on":
			out = append(out, regexp.MustCompile(`\b`+regexp.QuoteMeta(name)+`\b`))
		case r.Kind == kindEvent:
		default:
			prefix := ""
			if genericNames[name] {
				prefix = receiver
			}
			out = append(out, regexp.MustCompile(prefix+`\s*(?:\.|::)\s*`+regexp.QuoteMeta(name)+`\b`))
		}
	}
	if r.Kind == kindEvent {
		out = append(out, regexp.MustCompile(`["']`+regexp.QuoteMeta(r.Event)+`["']`))
	}
	return out
}

// consistencyRow is one extension API member or event with each runtime's
// presence and conformance reach.
type consistencyRow struct {
	Key       string
	Group     string
	Exercised map[string]bool
	Present   map[string]bool
}

func (m *matrix) consistencyRows(corpora map[string]string) []consistencyRow {
	var rows []consistencyRow
	for _, r := range m.rows {
		if isPackageRow(r) || (r.Kind != kindMember && r.Kind != kindEvent && r.Kind != kindPig) {
			continue
		}
		row := consistencyRow{Key: r.Key, Group: r.Group, Exercised: map[string]bool{}, Present: map[string]bool{}}
		for _, sdk := range sdks {
			c := r.Cells[sdk]
			if c.Status != statusImplemented && c.Status != statusPartial {
				continue
			}
			row.Present[sdk] = true
			for _, re := range exerciseTokens(r, c) {
				if re.MatchString(corpora[sdk]) {
					row.Exercised[sdk] = true
					break
				}
			}
			// Every SDK's fixture registers any event by name through its
			// event_probe_on command, so an event a conformance test emits by name
			// reaches the handler of every SDK.
			if r.Kind == kindEvent && strings.Contains(corpora[sdk], eventProbeCommand) && quoted(r.Event).MatchString(corpora[testsCorpus]) {
				row.Exercised[sdk] = true
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// eventProbeCommand is the fixture command that subscribes an SDK's handler to an
// event named by its argument.
const eventProbeCommand = "event_probe_on"

// testsCorpus keys the text of every conformance test file, which names the
// events a test emits.
const testsCorpus = "tests"

func quoted(name string) *regexp.Regexp {
	return regexp.MustCompile(`["']` + regexp.QuoteMeta(name) + `["']`)
}

func loadCorpora(root string) (map[string]string, error) {
	corpora := map[string]string{}
	var tests strings.Builder
	err := filepath.WalkDir(filepath.Join(root, "test/extension-conformance"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, err := os.ReadFile(path)
		tests.Write(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	corpora[testsCorpus] = tests.String()
	for _, sdk := range sdks {
		text, err := loadCorpus(root, sdk)
		if err != nil {
			return nil, fmt.Errorf("%s conformance corpus: %w", sdk, err)
		}
		corpora[sdk] = text
	}
	return corpora, nil
}

func loadGaps(root string) ([]gapSpec, error) {
	var gf gapsFile
	if _, err := toml.DecodeFile(filepath.Join(root, gapsPath), &gf); err != nil {
		return nil, err
	}
	return gf.Gaps, nil
}

// validateGaps fails on a surface an SDK implements and no conformance fixture
// reaches that the ledger does not list, and on a ledger entry that no longer
// names a hole, so the ledger only shrinks.
func validateGaps(rows []consistencyRow, gaps []gapSpec) error {
	var problems []string
	listed := map[string]map[string]bool{}
	byKey := map[string]consistencyRow{}
	for _, r := range rows {
		byKey[r.Key] = r
	}
	for _, g := range gaps {
		if strings.TrimSpace(g.Owner) == "" || strings.TrimSpace(g.Row) == "" || strings.TrimSpace(g.Reason) == "" {
			problems = append(problems, fmt.Sprintf("%s: gap %q needs owner, row and reason", gapsPath, g.Key))
		}
		row, ok := byKey[g.Key]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: gap %q names no member or event row", gapsPath, g.Key))
			continue
		}
		if listed[g.Key] == nil {
			listed[g.Key] = map[string]bool{}
		}
		for _, sdk := range g.SDKs {
			if _, known := sdkTitle[sdk]; !known {
				problems = append(problems, fmt.Sprintf("%s: gap %q names unknown runtime %q", gapsPath, g.Key, sdk))
				continue
			}
			if listed[g.Key][sdk] {
				problems = append(problems, fmt.Sprintf("%s: gap %q lists %s twice", gapsPath, g.Key, sdkTitle[sdk]))
			}
			listed[g.Key][sdk] = true
			if !row.Present[sdk] || row.Exercised[sdk] {
				problems = append(problems, fmt.Sprintf("%s: gap %q for %s is stale: delete it", gapsPath, g.Key, sdkTitle[sdk]))
			}
		}
	}
	for _, r := range rows {
		for _, sdk := range sdks {
			if r.Present[sdk] && !r.Exercised[sdk] && !listed[r.Key][sdk] {
				problems = append(problems, fmt.Sprintf("%s: no %s conformance fixture reaches it and %s lists no gap", r.Key, sdkTitle[sdk], gapsPath))
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%d conformance problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	return nil
}

func renderConsistency(rows []consistencyRow, gaps []gapSpec) []byte {
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("<!-- Code generated by go run ./test/parity/cmd/sdksurface; DO NOT EDIT. -->\n\n")
	w("# Extension SDK consistency matrix\n\n")
	w("For every Pi extension API member and event (and every PiG additive surface), whether each runtime has it ([`extension-sdk-surface.md`](extension-sdk-surface.md)) and whether a conformance fixture for that runtime reaches it.\n\n")
	w("- `exercised`: the runtime implements the surface and a fixture or scenario driver under `test/extension-conformance` that the conformance suite loads into the real host references its realization (a member access of the symbol's name, or an event's constant or quoted name; for an additive surface the extension implements and the host calls, a definition of the member).\n")
	w("- `present`: implemented, and no conformance fixture reaches it. Every one is a hole listed in [`%s`](../%s) with an owner and the row that closes it; the generator fails on an unlisted hole and on a listed one that closed.\n", gapsPath, gapsPath)
	w("- `-`: the runtime has no realization (see the surface matrix exceptions).\n\n")
	w("`exercised` is a reach check, not a behavior proof: a conformance test that reaches a surface must still assert its result. The behavioral rows are the scenario table (`test/extension-conformance/sdkscenarios`), run once per SDK through the real host, and the per-area conformance tests.\n\n")
	w("Regenerate with `go run ./test/parity/cmd/sdksurface`.\n\n")

	type tally struct{ present, exercised int }
	totals := map[string]*tally{}
	for _, sdk := range sdks {
		totals[sdk] = &tally{}
	}
	for _, r := range rows {
		for _, sdk := range sdks {
			if r.Present[sdk] {
				totals[sdk].present++
				if r.Exercised[sdk] {
					totals[sdk].exercised++
				}
			}
		}
	}
	w("## Summary\n\n%d surfaces.\n\n| Runtime | implemented | exercised by a conformance fixture | holes |\n|---|---|---|---|\n", len(rows))
	for _, sdk := range sdks {
		t := totals[sdk]
		w("| %s | %d | %d | %d |\n", sdkTitle[sdk], t.present, t.exercised, t.present-t.exercised)
	}
	w("\n")

	cellText := func(r consistencyRow, sdk string) string {
		switch {
		case r.Exercised[sdk]:
			return "exercised"
		case r.Present[sdk]:
			return "present"
		}
		return "-"
	}
	for i, r := range rows {
		if i == 0 || r.Group != rows[i-1].Group {
			w("## %s\n\n| Surface | Node runtime | Go | Rust | Python |\n|---|---|---|---|---|\n", r.Group)
		}
		w("| `%s` |", escapeCell(r.Key))
		for _, sdk := range sdks {
			w(" %s |", cellText(r, sdk))
		}
		w("\n")
		if i+1 == len(rows) || rows[i+1].Group != r.Group {
			w("\n")
		}
	}

	w("## Holes\n\n")
	if len(gaps) == 0 {
		w("None.\n")
		return b.Bytes()
	}
	w("| Surface | Runtimes | Owner | Closing row | Reason |\n|---|---|---|---|---|\n")
	for _, g := range gaps {
		var names []string
		for _, sdk := range g.SDKs {
			names = append(names, sdkTitle[sdk])
		}
		w("| `%s` | %s | %s | %s | %s |\n", escapeCell(g.Key), strings.Join(names, ", "), escapeCell(g.Owner), escapeCell(g.Row), escapeCell(g.Reason))
	}
	return b.Bytes()
}
