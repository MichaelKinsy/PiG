package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// The export ledger is the standing guard for the package half of Pi's
// extension surface: every runtime export, and every public class member, of
// the modules Pi serves to extensions gets one disposition per PiG runtime, so
// a Node-served export that no native SDK realizes cannot hide. It extends the
// same generator as the API matrix; test/parity/sdk-exports.toml is its input
// and docs/extension-sdk-exports.md its readable output.
const (
	ledgerPath     = "test/parity/sdk-exports.toml"
	usagePath      = "test/parity/extension-import-usage.json"
	exportsDocPath = "docs/extension-sdk-exports.md"
)

// divergenceDocs hold the headings that define numbered divergences.
var divergenceDocs = []string{"docs/parity/DIVERGENCES.md", "docs/additive-features.md"}

// ledgerModules are the specifiers the ledger covers: the modules Pi's
// extension loader serves from its own bundle, less the providers/all barrel
// (a registration side effect, not an API an extension calls).
var ledgerModules = []string{"pi-coding-agent", "pi-tui", "pi-ai", "pi-ai/compat", "pi-agent-core"}

// nativeSDKs are the ledger columns the author fills in; the Node column is
// derived from the runtime probe.
var nativeSDKs = []string{"go", "rust", "python"}

// Cell kinds. A cell is "<kind>" or "<kind>:<value>"; nothing else parses, so
// there is no spelling for n/a, deferred or pending.
const (
	cellNative       = "native"       // a symbol of the SDK, looked up in its source
	cellKit          = "kit"          // a declarative view of the SDK's component kit
	cellHost         = "host"         // the same behavior through a context call, named by its symbol
	cellIdentity     = "identity"     // pure type or alias: no runtime to realize
	cellDivergence   = "divergence"   // an owner-approved numbered divergence, D<n>
	cellGap          = "gap"          // not realized; the named lane owns closing it
	cellUnclassified = "unclassified" // a row the generator just added; fails until classified
)

var divergenceRE = regexp.MustCompile(`^D[0-9]+$`)

type ledgerCell struct{ Kind, Value string }

func parseCell(s string) (ledgerCell, error) {
	kind, value, hasValue := strings.Cut(s, ":")
	switch kind {
	case cellIdentity, cellUnclassified:
		if hasValue {
			return ledgerCell{}, fmt.Errorf("%q takes no value", kind)
		}
		return ledgerCell{Kind: kind}, nil
	case cellNative, cellKit, cellHost, cellDivergence, cellGap:
		if !hasValue || strings.TrimSpace(value) != value || value == "" || strings.ContainsAny(value, " \t") {
			return ledgerCell{}, fmt.Errorf("%q needs a value without spaces: %s:<value>", kind, kind)
		}
		if kind == cellDivergence && !divergenceRE.MatchString(value) {
			return ledgerCell{}, fmt.Errorf("divergence value %q is not a D<n> number", value)
		}
		return ledgerCell{Kind: kind, Value: value}, nil
	}
	return ledgerCell{}, fmt.Errorf("%q is not a disposition: use native:, kit:, host:, identity, divergence:D<n> or gap:<lane>", s)
}

func (c ledgerCell) String() string {
	if c.Value == "" {
		return c.Kind
	}
	return c.Kind + ":" + c.Value
}

// ledgerPolicy is the release policy the checks apply.
type ledgerPolicy struct {
	// ReleaseTrain is the train being built; Trains lists every train a lane
	// may name, oldest first.
	ReleaseTrain string   `toml:"release_train"`
	Trains       []string `toml:"trains"`
	// MaxGapTrains is how many trains a lane's gap may stay open past the one
	// that opened it.
	MaxGapTrains int `toml:"max_gap_trains"`
	// HighUse is the number of scanned packages importing an export at which a
	// gap on it blocks a release.
	HighUse int `toml:"high_use"`
	// IdentityRuntime lists the runtime exports whose identity disposition is
	// real (a helper that returns its argument); any other runtime export
	// needs a realization, a divergence or a gap.
	IdentityRuntime []string `toml:"identity_runtime"`
}

// ledgerLane is a lane that owns closing gaps.
type ledgerLane struct {
	Owner   string `toml:"owner"`
	Opened  string `toml:"opened"`
	Closing string `toml:"closing"`
}

// unreachedSpec is a reviewed hole: a realized cell no conformance fixture or
// vector test reaches. The list only shrinks.
type unreachedSpec struct {
	Key  string   `toml:"key"`
	SDKs []string `toml:"sdks"`
	Lane string   `toml:"lane"`
	Row  string   `toml:"row"`
}

type ledgerFile struct {
	Policy    ledgerPolicy                 `toml:"policy"`
	Lane      map[string]ledgerLane        `toml:"lane"`
	Unreached []unreachedSpec              `toml:"unreached"`
	Export    map[string]map[string]string `toml:"export"`
}

// ledgerRow is one denominator row: a runtime export or class member.
type ledgerRow struct {
	Key    string
	Module string
	Name   string // export name, or Class.member
	Decl   string // class, function, variable, external, or "member"
	// Node is the probe's verdict: "served" (Pi's own code or bridged),
	// "stand-in" or "missing".
	Node       string
	NodeReason string
}

// usageFile is test/parity/extension-import-usage.json: how many of the
// scanned npm packages import each export, by module then name.
type usageFile struct {
	Scan  map[string]any            `json:"scan"`
	Usage map[string]map[string]int `json:"usage"`
}

func loadUsage(root string) (usageFile, error) {
	var u usageFile
	raw, err := os.ReadFile(filepath.Join(root, usagePath))
	if err != nil {
		return u, err
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return u, fmt.Errorf("%s: %w", usagePath, err)
	}
	return u, nil
}

// uses is the number of scanned packages importing the row's export. The
// loader serves the pi-ai root as its compat entry point, so a compat export
// counts both specifiers. A class member has no import of its own.
func (u usageFile) uses(r ledgerRow) int {
	if r.Decl == "member" {
		return 0
	}
	n := u.Usage[r.Module][r.Name]
	if r.Module == "pi-ai/compat" {
		n += u.Usage["pi-ai"][r.Name]
	}
	return n
}

func loadLedger(root string) (ledgerFile, error) {
	var f ledgerFile
	meta, err := toml.DecodeFile(filepath.Join(root, ledgerPath), &f)
	if err != nil {
		return f, fmt.Errorf("%s: %w", ledgerPath, err)
	}
	if u := meta.Undecoded(); len(u) > 0 {
		return f, fmt.Errorf("%s: unknown keys %v", ledgerPath, u)
	}
	return f, nil
}

// divergenceHeadingRE matches the heading that defines a numbered divergence.
var divergenceHeadingRE = regexp.MustCompile(`(?m)^#+ (D[0-9]+)\b`)

func loadDivergenceIDs(root string) (map[string]bool, error) {
	ids := map[string]bool{}
	for _, doc := range divergenceDocs {
		raw, err := os.ReadFile(filepath.Join(root, doc))
		if err != nil {
			return nil, err
		}
		for _, m := range divergenceHeadingRE.FindAllSubmatch(raw, -1) {
			ids[string(m[1])] = true
		}
	}
	return ids, nil
}

// ledgerRows turns the package rows of the matrix into the ledger's
// denominator, for the ledger's modules only.
func ledgerRows(m *matrix) []ledgerRow {
	var out []ledgerRow
	for _, r := range m.rows {
		if !isPackageRow(r) {
			continue
		}
		module := strings.TrimPrefix(r.Group, "Package exports: ")
		if !slices.Contains(ledgerModules, module) {
			continue
		}
		row := ledgerRow{Key: r.Key, Module: module, Name: strings.TrimPrefix(r.Key, module+": "), Decl: "member"}
		if r.Kind == kindExport {
			row.Decl, _, _ = strings.Cut(r.Decl, " ")
		}
		switch c := r.Cells["node"]; c.Status {
		case statusVendored, statusBridged:
			row.Node = "served"
		case statusStandIn:
			row.Node, row.NodeReason = "stand-in", c.Note
		default:
			row.Node = "missing"
		}
		out = append(out, row)
	}
	return out
}

// ledgerInput is everything the checks read, so a test can seed it.
type ledgerInput struct {
	Rows        []ledgerRow
	File        ledgerFile
	Symbols     map[string]symbolSet // go, rust, python: every symbol, the component kit's as kit.Name (kit::Name in Rust)
	Corpus      map[string]string    // sdk: conformance fixtures and vector tests
	Usage       usageFile
	Divergences map[string]bool
	// Release adds the checks only a release gate makes: a gap on a high-use
	// export blocks it.
	Release bool
}

// kitPrefix qualifies a component kit view in an SDK's symbols.
var kitPrefix = map[string]string{"go": "kit.", "rust": "kit::", "python": "kit."}

var lastIdent = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*$`)

// reachPatterns are the patterns that show a corpus references a realized
// cell: its final identifier behind a member access, or, for a function or
// view, called or constructed. A kit view must be named through the kit.
func reachPatterns(c ledgerCell) []*regexp.Regexp {
	name := lastIdent.FindString(c.Value)
	if name == "" {
		return nil
	}
	q := regexp.QuoteMeta(name)
	if c.Kind == cellKit {
		return []*regexp.Regexp{
			regexp.MustCompile(`\bkit\w*\s*(?:\.|::)\s*(?:New)?` + q + `\b`),
			regexp.MustCompile(`\b` + q + `\s*(?:::new\b|::[a-z_]+\s*\(|\(|\{)`),
		}
	}
	prefix := `\b`
	if genericNames[name] {
		prefix = receiver + `\s*(?:\.|::)\s*`
		// A member of a named class is also reached through that class (`Store.set(...)`, `Store::set(...)`).
		if class := memberClass(c.Value); class != "" {
			return []*regexp.Regexp{
				regexp.MustCompile(prefix + q + `\b`),
				regexp.MustCompile(`\b` + regexp.QuoteMeta(class) + `\s*(?:\.|::)\s*` + q + `\b`),
			}
		}
	}
	return []*regexp.Regexp{regexp.MustCompile(prefix + q + `\b`)}
}

// memberClass is the type a qualified member symbol belongs to: "Store" for Store.set, Store::set and tui.Store.set;
// "" for a free function or a package-qualified one (tui.visible_width has no class).
func memberClass(symbol string) string {
	parts := strings.FieldsFunc(symbol, func(r rune) bool { return r == '.' || r == ':' })
	if len(parts) < 2 {
		return ""
	}
	class := parts[len(parts)-2]
	if class == "" || class[0] < 'A' || class[0] > 'Z' {
		return ""
	}
	return class
}

func reached(c ledgerCell, corpus string) bool {
	for _, re := range reachPatterns(c) {
		if re.MatchString(corpus) {
			return true
		}
	}
	// A generic member name called on a variable (`store.set(...)`) counts when the corpus also names the member's class.
	if name := lastIdent.FindString(c.Value); c.Kind != cellKit && genericNames[name] {
		if class := memberClass(c.Value); class != "" &&
			regexp.MustCompile(`\b`+regexp.QuoteMeta(class)+`\b`).MatchString(corpus) &&
			regexp.MustCompile(`[\w)\]]\s*(?:\.|::)\s*`+regexp.QuoteMeta(name)+`\s*\(`).MatchString(corpus) {
			return true
		}
	}
	return false
}

func trainIndex(p ledgerPolicy, train string) int { return slices.Index(p.Trains, train) }

// checkLedger returns every way the ledger fails the standing guard.
func checkLedger(in ledgerInput) []string {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	f := in.File
	p := f.Policy

	if p.MaxGapTrains < 0 || p.HighUse <= 0 {
		add("%s [policy]: max_gap_trains must be >= 0 and high_use > 0", ledgerPath)
	}
	cur := trainIndex(p, p.ReleaseTrain)
	if cur < 0 {
		add("%s [policy]: release_train %q is not in trains", ledgerPath, p.ReleaseTrain)
	}

	referenced := map[string]bool{}
	byKey := map[string]ledgerRow{}
	for _, r := range in.Rows {
		byKey[r.Key] = r
	}
	for key := range f.Export {
		if _, ok := byKey[key]; !ok {
			add("%s: %q is not an export of Pi's served modules; delete the row", ledgerPath, key)
		}
	}

	type realization struct {
		row  ledgerRow
		sdk  string
		cell ledgerCell
	}
	var realized []realization
	for _, r := range in.Rows {
		entry, ok := f.Export[r.Key]
		if !ok {
			add("%s: %s is unlisted: classify it for every runtime (run `go run ./test/parity/cmd/sdksurface` to add the row, then replace each %q)", ledgerPath, r.Key, cellUnclassified)
			continue
		}
		for field := range entry {
			if field != "node" && !slices.Contains(nativeSDKs, field) {
				add("%s: %s has unknown field %q", ledgerPath, r.Key, field)
			}
		}
		for _, sdk := range append([]string{"node"}, nativeSDKs...) {
			raw, has := entry[sdk]
			if sdk == "node" {
				switch {
				case r.Node == "served" && has:
					add("%s: %s Node cell %q is stale: the runtime serves it", ledgerPath, r.Key, raw)
					continue
				case r.Node == "served":
					continue
				case !has:
					add("%s: %s: the Node runtime does not serve it (%s) and the row has no Node cell", ledgerPath, r.Key, r.Node)
					continue
				}
			} else if !has {
				add("%s: %s has no %s cell", ledgerPath, r.Key, sdk)
				continue
			}
			c, err := parseCell(raw)
			if err != nil {
				add("%s: %s %s: %v", ledgerPath, r.Key, sdk, err)
				continue
			}
			switch c.Kind {
			case cellUnclassified:
				add("%s: %s %s is unclassified", ledgerPath, r.Key, sdk)
			case cellIdentity:
				if !slices.Contains(p.IdentityRuntime, r.Key) {
					add("%s: %s %s is identity, but only a type has no runtime; list it in policy identity_runtime if it is a helper that returns its argument", ledgerPath, r.Key, sdk)
				}
			case cellDivergence:
				if !in.Divergences[c.Value] {
					add("%s: %s %s names %s, which no heading of %s defines", ledgerPath, r.Key, sdk, c.Value, strings.Join(divergenceDocs, " or "))
				}
			case cellGap:
				if _, ok := f.Lane[c.Value]; !ok {
					add("%s: %s %s names lane %q, which has no [lane.%s] table", ledgerPath, r.Key, sdk, c.Value, c.Value)
					break
				}
				referenced[c.Value] = true
				if in.Release && p.HighUse > 0 && in.Usage.uses(r) >= p.HighUse {
					add("release: %s %s is a gap (lane %s) on an export %d scanned packages import (threshold %d)", r.Key, sdk, c.Value, in.Usage.uses(r), p.HighUse)
				}
			case cellNative, cellHost, cellKit:
				if sdk == "node" {
					add("%s: %s Node cell cannot be %s: the Node runtime serves Pi's own code", ledgerPath, r.Key, c.Kind)
					break
				}
				sym := c.Value
				if c.Kind == cellKit {
					sym = kitPrefix[sdk] + sym
				}
				if !in.Symbols[sdk].has(sym) {
					add("%s: %s %s names %s %q, which the %s SDK does not define", ledgerPath, r.Key, sdk, c.Kind, c.Value, sdkTitle[sdk])
					break
				}
				realized = append(realized, realization{r, sdk, c})
			}
		}
	}

	// Reach: every realized cell is exercised, or is a reviewed hole.
	holes := map[string]map[string]bool{}
	for _, u := range f.Unreached {
		if strings.TrimSpace(u.Row) == "" {
			add("%s: unreached %q needs the row that closes it", ledgerPath, u.Key)
		}
		if _, ok := f.Lane[u.Lane]; !ok {
			add("%s: unreached %q names lane %q, which has no [lane.%s] table", ledgerPath, u.Key, u.Lane, u.Lane)
		} else {
			referenced[u.Lane] = true
		}
		if holes[u.Key] == nil {
			holes[u.Key] = map[string]bool{}
		}
		for _, sdk := range u.SDKs {
			if !slices.Contains(nativeSDKs, sdk) {
				add("%s: unreached %q names unknown runtime %q", ledgerPath, u.Key, sdk)
				continue
			}
			if holes[u.Key][sdk] {
				add("%s: unreached %q lists %s twice", ledgerPath, u.Key, sdkTitle[sdk])
			}
			holes[u.Key][sdk] = true
		}
	}
	live := map[string]map[string]bool{}
	for _, re := range realized {
		isReached := reached(re.cell, in.Corpus[re.sdk])
		if live[re.row.Key] == nil {
			live[re.row.Key] = map[string]bool{}
		}
		live[re.row.Key][re.sdk] = true
		listed := holes[re.row.Key][re.sdk]
		switch {
		case !isReached && !listed:
			add("%s: %s %s (%s) is reached by no conformance fixture or vector test and %s lists no [[unreached]] for it", ledgerPath, re.row.Key, re.sdk, re.cell, ledgerPath)
		case isReached && listed:
			add("%s: unreached %q for %s is stale: a fixture or vector test reaches it now; delete it", ledgerPath, re.row.Key, sdkTitle[re.sdk])
		}
	}
	for _, u := range f.Unreached {
		for _, sdk := range u.SDKs {
			if !live[u.Key][sdk] {
				add("%s: unreached %q for %s names no realized cell; delete it", ledgerPath, u.Key, sdkTitle[sdk])
			}
		}
	}
	// Lanes: complete, current, and still owning a gap.
	laneNames := make([]string, 0, len(f.Lane))
	for name := range f.Lane {
		laneNames = append(laneNames, name)
	}
	sort.Strings(laneNames)
	for _, name := range laneNames {
		l := f.Lane[name]
		if strings.TrimSpace(l.Owner) == "" || strings.TrimSpace(l.Closing) == "" {
			add("%s [lane.%s]: owner and closing (the conformance row, test or vector set that closes it) are required", ledgerPath, name)
		}
		opened := trainIndex(p, l.Opened)
		switch {
		case opened < 0:
			add("%s [lane.%s]: opened %q is not in policy trains", ledgerPath, name, l.Opened)
		case cur >= 0 && cur-opened > p.MaxGapTrains:
			add("%s [lane.%s]: its gaps are %d train(s) old (opened %s, current %s, limit %d): close them or the release is blocked", ledgerPath, name, cur-opened, l.Opened, p.ReleaseTrain, p.MaxGapTrains)
		}
		if !referenced[name] {
			add("%s [lane.%s]: no gap names this lane any more; delete it", ledgerPath, name)
		}
	}

	sort.Strings(problems)
	return slices.Compact(problems)
}

// ── Rendering ───────────────────────────────────────────────────────────────

const ledgerHeader = `# Disposition of every runtime export (and public class member) of the modules Pi
# serves to extensions (pi-coding-agent, pi-tui, pi-ai, pi-ai/compat,
# pi-agent-core) in each PiG extension runtime. Type-only exports (interfaces,
# type aliases) have no runtime and are identity by kind: they take no row.
#
# The generator (go run ./test/parity/cmd/sdksurface) writes this file in one
# canonical layout, keeps every cell you set, adds a row for each new Pi export
# with every cell "unclassified" and drops rows Pi no longer exports. The check
# fails until each cell is one of:
#
#   native:<symbol>      a symbol of the SDK (Go Name, Rust snake_case, Python snake_case)
#                        a public subpackage's symbol carries its qualifier: tui.VisibleWidth (Go), tui::visible_width (Rust), tui.visible_width (Python)
#   kit:<view>           a declarative component-kit view
#   host:<symbol>        the same behavior through a context call
#   identity             a pure type or alias (only for listed policy identity_runtime helpers)
#   divergence:D<n>      an owner-approved divergence in docs/parity/DIVERGENCES.md
#   gap:<lane>           not realized; [lane.<lane>] names the owner and the closing row
#
# There is no n/a, deferred or pending. A "node" cell appears only where the
# Node runtime does not serve the export. Every native, kit and host cell must be
# reached by a conformance fixture (test/extension-conformance) or by a vector
# test generated from Pi's own implementation (a test file under the SDK that
# reads test/parity/sdk-vectors/); a cell that is not yet reached is listed in
# [[unreached]], which only shrinks. A gap on an export that
# test/parity/extension-import-usage.json shows high_use or more scanned packages
# importing blocks the release gate (make sdk-exports-release), and so does a lane
# older than max_gap_trains.
`

func tomlString(s string) string { return strconv.Quote(s) }

func renderLedger(f ledgerFile, rows []ledgerRow) []byte {
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	b.WriteString(ledgerHeader)
	w("\n[policy]\nrelease_train = %s\ntrains = [", tomlString(f.Policy.ReleaseTrain))
	for i, t := range f.Policy.Trains {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(tomlString(t))
	}
	w("]\nmax_gap_trains = %d\nhigh_use = %d\nidentity_runtime = [", f.Policy.MaxGapTrains, f.Policy.HighUse)
	identity := slices.Clone(f.Policy.IdentityRuntime)
	sort.Strings(identity)
	for i, k := range identity {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(tomlString(k))
	}
	w("]\n")
	names := make([]string, 0, len(f.Lane))
	for n := range f.Lane {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		l := f.Lane[n]
		w("\n[lane.%s]\nowner = %s\nopened = %s\nclosing = %s\n", n, tomlString(l.Owner), tomlString(l.Opened), tomlString(l.Closing))
	}
	holes := slices.Clone(f.Unreached)
	sort.Slice(holes, func(i, j int) bool { return holes[i].Key < holes[j].Key })
	for _, u := range holes {
		sdks := make([]string, len(u.SDKs))
		for i, s := range u.SDKs {
			sdks[i] = tomlString(s)
		}
		w("\n[[unreached]]\nkey = %s\nsdks = [%s]\nlane = %s\nrow = %s\n", tomlString(u.Key), strings.Join(sdks, ", "), tomlString(u.Lane), tomlString(u.Row))
	}
	w("\n[export]\n")
	for _, r := range rows {
		old := f.Export[r.Key]
		var parts []string
		if r.Node != "served" {
			v := old["node"]
			if v == "" {
				v = cellUnclassified
			}
			parts = append(parts, "node = "+tomlString(v))
		}
		for _, sdk := range nativeSDKs {
			v := old[sdk]
			if v == "" {
				v = cellUnclassified
			}
			parts = append(parts, sdk+" = "+tomlString(v))
		}
		w("%s = { %s }\n", tomlString(r.Key), strings.Join(parts, ", "))
	}
	return b.Bytes()
}

func (in ledgerInput) count(sdk string, rows []ledgerRow) map[string]int {
	out := map[string]int{}
	for _, r := range rows {
		raw := in.File.Export[r.Key][sdk]
		if sdk == "node" && r.Node == "served" {
			out["served"]++
			continue
		}
		c, err := parseCell(raw)
		if err != nil {
			out["invalid"]++
			continue
		}
		out[c.Kind]++
	}
	return out
}

// renderExportsDoc is the readable form of the ledger.
func renderExportsDoc(in ledgerInput, typeOnly map[string]int) []byte {
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("<!-- Code generated by go run ./test/parity/cmd/sdksurface; DO NOT EDIT. -->\n\n# Extension SDK export ledger\n\n")
	w("The disposition of every runtime export and public class member of the modules Pi serves to extensions, in each PiG runtime. ")
	w("The input is [`%s`](../%s); the rules (what a cell may say, the reach check, the gap policy) are in its header. `make sdk-surface-drift` fails on an unlisted or unclassified row, a gap without a lane, and a realized cell no conformance fixture or vector test reaches; `make sdk-exports-release` also fails on a gap on a high-use export or a lane past its train limit.\n\n", ledgerPath, ledgerPath)
	w("Use counts (`uses`) are the number of the %v scanned npm packages that import the export ([`%s`](../%s)); a class member has none of its own.\n\n", in.Usage.Scan["packages"], usagePath, usagePath)

	byModule := map[string][]ledgerRow{}
	for _, r := range in.Rows {
		byModule[r.Module] = append(byModule[r.Module], r)
	}
	w("## Summary\n\n| Module | Rows | type-only (identity by kind) | Node served | Go gaps | Rust gaps | Python gaps |\n|---|---|---|---|---|---|---|\n")
	for _, m := range ledgerModules {
		rows := byModule[m]
		served := 0
		for _, r := range rows {
			if r.Node == "served" {
				served++
			}
		}
		w("| `%s` | %d | %d | %d |", m, len(rows), typeOnly[m], served)
		for _, sdk := range nativeSDKs {
			w(" %d |", in.count(sdk, rows)[cellGap])
		}
		w("\n")
	}
	w("\n## Lanes that own gaps\n\n| Lane | Owner | Opened | Gap cells | Closing row |\n|---|---|---|---|---|\n")
	laneCells := map[string]int{}
	for _, r := range in.Rows {
		for _, sdk := range append([]string{"node"}, nativeSDKs...) {
			if c, err := parseCell(in.File.Export[r.Key][sdk]); err == nil && c.Kind == cellGap {
				laneCells[c.Value]++
			}
		}
	}
	names := make([]string, 0, len(in.File.Lane))
	for n := range in.File.Lane {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		l := in.File.Lane[n]
		w("| %s | %s | %s | %d | %s |\n", n, escapeCell(l.Owner), l.Opened, laneCells[n], escapeCell(l.Closing))
	}

	type gapRow struct {
		row  ledgerRow
		uses int
		sdks []string
	}
	var high []gapRow
	for _, r := range in.Rows {
		var sdks []string
		for _, sdk := range append([]string{"node"}, nativeSDKs...) {
			if c, err := parseCell(in.File.Export[r.Key][sdk]); err == nil && c.Kind == cellGap {
				sdks = append(sdks, sdkTitle[sdk]+" "+c.Value)
			}
		}
		if u := in.Usage.uses(r); len(sdks) > 0 && u >= in.File.Policy.HighUse {
			high = append(high, gapRow{r, u, sdks})
		}
	}
	sort.SliceStable(high, func(i, j int) bool { return high[i].uses > high[j].uses })
	w("\n## High-use gaps (block the release gate)\n\n")
	if len(high) == 0 {
		w("None.\n")
	} else {
		w("%d exports imported by %d or more scanned packages have a gap.\n\n| Export | uses | Gaps |\n|---|---|---|\n", len(high), in.File.Policy.HighUse)
		for _, g := range high {
			w("| `%s` | %d | %s |\n", escapeCell(g.row.Key), g.uses, strings.Join(g.sdks, "; "))
		}
	}
	w("\n## Realized cells no fixture reaches\n\n")
	if len(in.File.Unreached) == 0 {
		w("None.\n")
	} else {
		w("| Export | Runtimes | Lane | Closing row |\n|---|---|---|---|\n")
		holes := slices.Clone(in.File.Unreached)
		sort.Slice(holes, func(i, j int) bool { return holes[i].Key < holes[j].Key })
		for _, u := range holes {
			w("| `%s` | %s | %s | %s |\n", escapeCell(u.Key), strings.Join(u.SDKs, ", "), u.Lane, escapeCell(u.Row))
		}
	}
	for _, m := range ledgerModules {
		w("\n## %s\n\n| Export | uses | Node | Go | Rust | Python |\n|---|---|---|---|---|---|\n", m)
		for _, r := range byModule[m] {
			w("| `%s` | %d |", escapeCell(r.Name), in.Usage.uses(r))
			for _, sdk := range append([]string{"node"}, nativeSDKs...) {
				cell := in.File.Export[r.Key][sdk]
				if sdk == "node" && r.Node == "served" {
					cell = "served"
				}
				w(" %s |", escapeCell(cell))
			}
			w("\n")
		}
	}
	return b.Bytes()
}
