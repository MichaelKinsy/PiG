// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Row statuses shown in the port map.
const (
	StatusPorted      = "ported"
	StatusDerived     = "derived"
	StatusDesignedOut = "designed-out"
	StatusGap         = "gap"
)

// Proof classes (the required behaviour-proof class of a closed row, test/parity/interface-closure/breakdown/proofclass.py).
const (
	ClassDesignOut    = "D"
	ClassWire         = "W"
	ClassRegistration = "R"
	ClassUnion        = "U"
	ClassBehaviour    = "F"
	ClassNone         = "N"
)

// Row is one upstream interface with its Go mapping.
type Row struct {
	ID          string
	Pkg         string
	Kind        string
	Name        string
	Status      string
	Class       string
	Rationale   string
	TSFile      string
	SourcePath  string
	Line        int
	Family      string
	Targets     []Ref
	Production  []RefKind
	Tests       []Ref
	Reach       []Ref
	Layers      map[string]string
	Async       string
	Contracts   []string
	GapReason   string
	Divergences []string
	PiDecl      string
	// Unproven is the reason the behaviour-proof tool lists the row as unproven; Proven is true only when that tool's output was read and does not list the row.
	Unproven string
	Proven   bool
}

// RefKind is a production reference with the way it reaches the target (call, public-api, tested).
type RefKind struct {
	Kind string
	Ref
}

// PinLedger is the port map of one upstream pin.
type PinLedger struct {
	Version   string
	Committed bool
	Rows      []*Row
	// Counts by package and status.
	Packages []string
	Total    int
	// GapBaseline is the number of rows each package lists in the committed gap baseline file.
	GapBaseline     map[string]int
	GapBaselineRows int
	ProofBaseline   map[string]int
	ProofRead       bool
}

type mappingFile struct {
	UpstreamVersion string       `json:"upstreamVersion"`
	Mappings        []mappingRow `json:"mappings"`
}

type mappingRow struct {
	ID          string            `json:"id"`
	Disposition string            `json:"disposition"`
	Rationale   string            `json:"rationale"`
	PigTargets  []string          `json:"pigTargets"`
	Layers      map[string]string `json:"layers"`
	Production  []string          `json:"production"`
	Evidence    []string          `json:"evidence"`
	Async       string            `json:"asyncContract"`
	Contracts   []string          `json:"behaviorContracts"`
}

type inventoryFile struct {
	Interfaces []invRow `json:"interfaces"`
}

type invRow struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	ParentID string   `json:"parentId"`
	Shape    invShape `json:"shape"`
	Source   struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	} `json:"source"`
	// CLI rows carry the parser source instead.
	Parser struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	} `json:"parser"`
	Command string `json:"command"`
	Flag    string `json:"flag"`
	Value   string `json:"value"`
	Aliases []string
}

type invShape struct {
	Type          string     `json:"type"`
	AliasTarget   string     `json:"aliasTarget"`
	TypeParams    []invTP    `json:"typeParameters"`
	Calls         []invCall  `json:"calls"`
	Properties    []invProp  `json:"properties"`
	EnumMembers   []any      `json:"enumMembers"`
	Parameters    []invParam `json:"parameters"`
	Returns       string     `json:"returns"`
	ExtendsOrNote string     `json:"-"`
}

type invTP struct {
	Name       string `json:"name"`
	Constraint string `json:"constraint"`
	Default    string `json:"default"`
}

// typeParams renders <T extends C = D>.
func typeParams(tps []invTP) string {
	if len(tps) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tps))
	for _, t := range tps {
		s := t.Name
		if t.Constraint != "" {
			s += " extends " + t.Constraint
		}
		if t.Default != "" {
			s += " = " + t.Default
		}
		parts = append(parts, s)
	}
	return "<" + strings.Join(parts, ", ") + ">"
}

type invCall struct {
	TypeParams []invTP    `json:"typeParameters"`
	Parameters []invParam `json:"parameters"`
	Returns    string     `json:"returns"`
}

type invParam struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Optional bool   `json:"optional"`
	Rest     bool   `json:"rest"`
}

type invProp struct {
	Name     string `json:"name"`
	Optional bool   `json:"optional"`
	Readonly bool   `json:"readonly"`
	Type     string `json:"type"`
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

var (
	derivedPrefix = "Derived by the interface gap detector"
	dRef          = regexp.MustCompile(`\bD(\d{1,3})\b`)
	idParts       = regexp.MustCompile(`^(pkg:[^/]+|cli:[^/]+)/?([^#]*)#?(.*)$`)
)

// splitID returns the package short name ("agent", "cli") and the part after "#".
func splitID(id string) (pkg, entry, rest string) {
	m := idParts.FindStringSubmatch(id)
	if m == nil {
		return "?", "", id
	}
	head := m[1]
	switch {
	case strings.HasPrefix(head, "pkg:"):
		pkg = strings.TrimPrefix(head, "pkg:")
	default:
		pkg = "cli"
	}
	return pkg, m[2], m[3]
}

// parseRefs splits "kind:file#Sym" (or "file#Sym") references and resolves their lines.
func parseRef(s string, syms *symIndex) (kind string, r Ref) {
	if k, rest, ok := strings.Cut(s, ":"); ok && !strings.Contains(k, "/") && !strings.Contains(k, ".") {
		kind, s = k, rest
	}
	file, sym, _ := strings.Cut(s, "#")
	return kind, syms.resolve(file, sym)
}

// loadPinLedger reads the mapping, inventory and gap baseline of one pin.
func loadPinLedger(root, version string, committed bool, syms *symIndex, fams []family) (*PinLedger, error) {
	dir := filepath.Join(root, "test", "parity", "interfaces")
	var mf mappingFile
	if err := readJSON(filepath.Join(dir, "mapping-v"+version+".json"), &mf); err != nil {
		return nil, err
	}
	inv := map[string]*invRow{}
	var uf inventoryFile
	if err := readJSON(filepath.Join(dir, "upstream-v"+version+".json"), &uf); err == nil {
		for i := range uf.Interfaces {
			inv[uf.Interfaces[i].ID] = &uf.Interfaces[i]
		}
	}
	var cf inventoryFile
	if err := readJSON(filepath.Join(dir, "cli-v"+version+".json"), &cf); err == nil {
		for i := range cf.Interfaces {
			inv[cf.Interfaces[i].ID] = &cf.Interfaces[i]
		}
	}
	gaps := map[string][2]string{}
	gapPkg := map[string]int{}
	gapFile := filepath.Join(root, "test", "parity", "interface-closure", "gaps", "interface-gaps-v"+version+".tsv")
	gapLines := readLines(gapFile)
	for _, ln := range gapLines {
		c := strings.Split(ln, "\t")
		if len(c) < 3 {
			continue
		}
		gaps[c[0]] = [2]string{c[2], ""}
		gapPkg[c[1]]++
	}
	byID := map[string]*mappingRow{}
	for i := range mf.Mappings {
		byID[mf.Mappings[i].ID] = &mf.Mappings[i]
	}
	// The proof-class tool runs against the committed pin's ledger, so its output describes only that pin.
	var unproven map[string]string
	proofRead := false
	if committed {
		unproven, proofRead = readUnproven(root)
	}

	pl := &PinLedger{Version: version, Committed: committed, GapBaseline: gapPkg, GapBaselineRows: len(gapLines), ProofBaseline: map[string]int{}, ProofRead: proofRead}
	// The proof-class baseline is a header line and one package/count line per package; an absent file leaves the baseline empty.
	for i, ln := range readLines(filepath.Join(root, "test", "parity", "interface-closure", "gaps", "proof-class-baseline.tsv")) {
		if i == 0 {
			continue
		}
		if p, n, ok := strings.Cut(ln, "\t"); ok {
			v, _ := strconv.Atoi(n)
			pl.ProofBaseline[p] = v
		}
	}
	pkgSeen := map[string]bool{}
	for i := range mf.Mappings {
		m := &mf.Mappings[i]
		pkg, _, rest := splitID(m.ID)
		r := &Row{ID: m.ID, Pkg: pkg, Name: rest, Rationale: m.Rationale, Layers: m.Layers, Async: m.Async, Contracts: m.Contracts}
		if len(m.Contracts) > 1 {
			sort.Strings(r.Contracts)
		}
		switch {
		case m.Disposition == "designed-out":
			r.Status = StatusDesignedOut
		case m.Disposition == "pending":
			r.Status = StatusGap
		case strings.HasPrefix(m.Rationale, derivedPrefix):
			r.Status = StatusDerived
		default:
			r.Status = StatusPorted
		}
		if g, ok := gaps[m.ID]; ok {
			r.Status = StatusGap
			r.GapReason = g[0]
		} else if r.Status == StatusGap {
			r.GapReason = "pending"
		}
		for _, t := range m.PigTargets {
			_, ref := parseRef(t, syms)
			r.Targets = append(r.Targets, ref)
		}
		for _, t := range m.Production {
			k, ref := parseRef(t, syms)
			r.Production = append(r.Production, RefKind{Kind: k, Ref: ref})
		}
		for _, t := range m.Evidence {
			k, ref := parseRef(t, syms)
			if k == "reach" {
				r.Reach = append(r.Reach, ref)
			} else {
				r.Tests = append(r.Tests, ref)
			}
		}
		for _, d := range dRef.FindAllStringSubmatch(m.Rationale, -1) {
			r.Divergences = appendUnique(r.Divergences, "D"+d[1])
		}
		if iv := inv[m.ID]; iv != nil {
			r.Kind = iv.Kind
			applySource(r, iv)
			r.PiDecl = piDecl(iv, inv)
		}
		r.Family = familyOf(fams, r.TSFile)
		r.Class = proofClass(r, inv[m.ID], byID)
		if r.Status != StatusGap {
			if u, ok := unproven[m.ID]; ok {
				r.Unproven = u
			} else if proofRead {
				r.Proven = true
			}
		}
		pkgSeen[pkg] = true
		pl.Rows = append(pl.Rows, r)
	}
	for p := range pkgSeen {
		pl.Packages = append(pl.Packages, p)
	}
	sort.Strings(pl.Packages)
	sort.SliceStable(pl.Rows, func(i, j int) bool {
		a, b := pl.Rows[i], pl.Rows[j]
		if a.Pkg != b.Pkg {
			return a.Pkg < b.Pkg
		}
		if a.TSFile != b.TSFile {
			return a.TSFile < b.TSFile
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.ID < b.ID
	})
	pl.Total = len(pl.Rows)
	return pl, nil
}

func appendUnique(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}

var distRe = regexp.MustCompile(`^node_modules/@earendil-works/pi-([^/]+)/dist/(.+?)(\.d\.ts|\.ts|\.js)$`)

// applySource sets the upstream source file and line of a row. Published declarations map to the package source tree.
func applySource(r *Row, iv *invRow) {
	p, line := iv.Source.Path, iv.Source.Line
	if p == "" {
		p, line = iv.Parser.Path, iv.Parser.Line
	}
	r.SourcePath, r.Line = p, line
	r.TSFile = p
	if m := distRe.FindStringSubmatch(p); m != nil {
		r.TSFile = "packages/" + pkgDir(m[1]) + "/src/" + m[2] + ".ts"
	}
	if r.TSFile == "" {
		r.TSFile = "(no source)"
	}
}

// pkgDir maps a published package suffix to its directory under packages/.
func pkgDir(s string) string {
	switch s {
	case "agent-core":
		return "agent"
	case "ai":
		return "ai"
	}
	return s
}

type family struct {
	Name     string
	Includes []string
}

// loadFamilies reads the behavior family scopes.
func loadFamilies(root string) []family {
	var doc struct {
		Families map[string]struct {
			Includes []string `toml:"includes"`
		} `toml:"families"`
	}
	if _, err := toml.DecodeFile(filepath.Join(root, "test", "parity", "families.toml"), &doc); err != nil {
		return nil
	}
	names := make([]string, 0, len(doc.Families))
	for n := range doc.Families {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]family, 0, len(names))
	for _, n := range names {
		out = append(out, family{Name: n, Includes: doc.Families[n].Includes})
	}
	return out
}

// familyOf returns the first behavior family whose scope contains the upstream file.
func familyOf(fams []family, tsFile string) string {
	for _, f := range fams {
		for _, inc := range f.Includes {
			if inc == tsFile || (strings.HasSuffix(inc, "/") && strings.HasPrefix(tsFile, inc)) {
				return f.Name
			}
		}
	}
	return ""
}

// readUnproven reads the output of the proof-class tool when a run left it in build/ledger-proof-classes.
func readUnproven(root string) (map[string]string, bool) {
	lines := readLines(filepath.Join(root, "build", "ledger-proof-classes", "unproven-behaviour.tsv"))
	if len(lines) == 0 {
		return nil, false
	}
	out := map[string]string{}
	for _, ln := range lines[1:] {
		c := strings.Split(ln, "\t")
		if len(c) >= 4 {
			out[c[0]] = c[3]
		}
	}
	return out, true
}

var (
	wireTop = map[string]bool{"ExtensionAPI": true, "ExtensionContext": true, "ExtensionCommandContext": true, "ExtensionUIContext": true, "ExtensionActions": true,
		"ExtensionContextActions": true, "ExtensionToolContext": true, "ExtensionRuntime": true, "ExtensionRunner": true, "ExtensionFactory": true}
	regRe     = regexp.MustCompile(`(Registration|Definition|Declaration|Registered\w*|ProviderConfig|ProviderModelConfig|ExtensionFlag|ExtensionShortcut)$`)
	callToken = "::call:"
)

// topPipe reports whether a type text has a union bar outside any brackets.
func topPipe(t string) bool {
	depth := 0
	for _, ch := range t {
		switch ch {
		case '<', '(', '[', '{':
			depth++
		case '>', ')', ']', '}':
			depth--
		case '|':
			if depth == 0 {
				return true
			}
		}
	}
	return false
}

// proofClass derives the required behaviour-proof class of a row from its kind, as proofclass.py does.
func proofClass(r *Row, iv *invRow, byID map[string]*mappingRow) string {
	if r.Status == StatusDesignedOut {
		return ClassDesignOut
	}
	// The id of a package row reads "<entry>#<top>[::...]"; the class rules key on the top-level name.
	_, _, afterHash := splitID(r.ID)
	top, rest := afterHash, ""
	if i := strings.Index(afterHash, "::"); i >= 0 {
		top, rest = afterHash[:i], afterHash[i:]
	}
	kind := ""
	var shape invShape
	if iv != nil {
		kind, shape = iv.Kind, iv.Shape
	}
	if (wireTop[top] || strings.HasPrefix(top, "Rpc") || strings.HasSuffix(top, "Event")) && rest != "" {
		return ClassWire
	}
	if top != "" && top[0] >= 'A' && top[0] <= 'Z' && regRe.MatchString(top) && rest != "" {
		return ClassRegistration
	}
	if (kind == "type-alias" && topPipe(shape.Type)) || topPipe(shape.AliasTarget) {
		return ClassUnion
	}
	if kind == "function" || kind == "class" || kind == "construct-overload" || kind == "call-overload" || strings.HasSuffix(rest, "::construct:0") || strings.Contains(rest, callToken) {
		return ClassBehaviour
	}
	if kind == "property" {
		if _, ok := byID[r.ID+callToken+"0"]; ok {
			return ClassBehaviour
		}
	}
	return ClassNone
}

// piDecl renders the published declaration of a row as TypeScript text from the inventory shape.
func piDecl(iv *invRow, inv map[string]*invRow) string {
	sig := func(c invCall) string {
		ps := make([]string, 0, len(c.Parameters))
		for _, p := range c.Parameters {
			s := p.Name
			if p.Rest {
				s = "..." + s
			}
			if p.Optional {
				s += "?"
			}
			ps = append(ps, s+": "+p.Type)
		}
		tp := typeParams(c.TypeParams)
		return tp + "(" + strings.Join(ps, ", ") + "): " + c.Returns
	}
	_, _, rest := splitID(iv.ID)
	short := rest
	if i := strings.LastIndex(rest, "::"); i >= 0 {
		short = rest[i+2:]
	}
	switch iv.Kind {
	case "property":
		name := iv.Name
		if i := strings.LastIndex(iv.ID, "::property:"); i >= 0 {
			name = iv.ID[i+len("::property:"):]
		}
		return name + ": " + iv.Shape.Type + ";"
	case "call-overload":
		if len(iv.Shape.Calls) > 0 {
			return sig(iv.Shape.Calls[0]) + ";"
		}
		ps := make([]string, 0, len(iv.Shape.Parameters))
		for _, p := range iv.Shape.Parameters {
			ps = append(ps, p.Name+": "+p.Type)
		}
		return "(" + strings.Join(ps, ", ") + "): " + iv.Shape.Returns + ";"
	case "construct-overload":
		return "new " + iv.Name + ";"
	case "function":
		if len(iv.Shape.Calls) > 0 {
			return "function " + short + sig(iv.Shape.Calls[0]) + ";"
		}
		return "function " + short + ": " + iv.Shape.Type + ";"
	case "type-alias":
		return "type " + short + " = " + firstNonEmpty(iv.Shape.AliasTarget, iv.Shape.Type) + ";"
	case "variable":
		return "declare const " + short + ": " + iv.Shape.Type + ";"
	case "interface", "class":
		var sb strings.Builder
		tp := typeParams(iv.Shape.TypeParams)
		sb.WriteString(iv.Kind + " " + short + tp + " {\n")
		for i, p := range iv.Shape.Properties {
			if i >= 24 {
				fmt.Fprintf(&sb, "  // … %d more members\n", len(iv.Shape.Properties)-i)
				break
			}
			ro := ""
			if p.Readonly {
				ro = "readonly "
			}
			opt := ""
			if p.Optional {
				opt = "?"
			}
			fmt.Fprintf(&sb, "  %s%s%s: %s;\n", ro, p.Name, opt, p.Type)
		}
		sb.WriteString("}")
		return sb.String()
	}
	if iv.Flag != "" {
		s := iv.Command + " " + iv.Flag
		if iv.Value != "" {
			s += " " + iv.Value
		}
		return s
	}
	return path.Base(iv.ID)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
