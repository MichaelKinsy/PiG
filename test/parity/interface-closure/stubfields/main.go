// Command stubfields generates the missing plain-data fields of Go structs from the interface-gap list.
//
// For every gap row of reason member-missing whose upstream property belongs to an interface (plain data, not a class) and whose Go
// owner is a struct in the repository, it adds one field with a JSON tag. The Go name, type and tag follow fixed rules (see
// fieldFor); a property whose type has no rule is reported and left for a person: functions, classes, generics, conditional and
// mapped types, and named types the owner's package does not declare. The tool never edits a method, a function or a field that
// exists. Run `make go-stub-fields` for a report; `make go-stub-fields APPLY=1` writes the fields.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type entry struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Shape    struct {
		Name        string `json:"name"`
		Optional    bool   `json:"optional"`
		Type        string `json:"type"`
		AliasTarget string `json:"aliasTarget"`
		Calls       []any  `json:"calls"`
	} `json:"shape"`
	Source struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	} `json:"source"`
}

type gap struct{ id, reason, target string }

type options struct {
	root, gaps, inventory, report, labels, wiring, upstream string
	apply                                                   bool
}

func main() {
	var o options
	flag.StringVar(&o.root, "root", ".", "repository root")
	flag.StringVar(&o.gaps, "gaps", "build/interface-gaps/gaps.tsv", "gap list written by `make interface-gaps`")
	flag.StringVar(&o.inventory, "inventory", "test/parity/interfaces/upstream-v"+pigversion.UpstreamVersion+".json", "upstream interface inventory")
	flag.StringVar(&o.report, "report", "", "write the report here instead of standard output")
	flag.StringVar(&o.labels, "labels", "", "comma-separated label files or globs (ID, label, ...): only a row labelled MISSING-DATA is generated; a row with another label, or none, is left for a person")
	flag.StringVar(&o.wiring, "wiring", "", "TSV of ID, production Go file, evidence: where production code sets or reads the new field. -apply writes a field only for a row listed here whose file (not a test) selects or sets the field outside its declaration")
	flag.StringVar(&o.upstream, "upstream", ".upstream/current/packages", "pinned upstream package sources, searched for the citation of each generated field")
	flag.BoolVar(&o.apply, "apply", false, "write the generated fields into the Go sources")
	flag.Parse()
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "stubfields:", err)
		os.Exit(1)
	}
}

// documentedRenames reads the detector's rename tables.
func documentedRenames(root string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range []string{"renames.json", "renames-reviewed.json"} {
		raw, err := os.ReadFile(filepath.Join(root, "test/parity/interface-closure/autobind", name))
		if err != nil {
			return nil, err
		}
		var table map[string]string
		if err := json.Unmarshal(raw, &table); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		maps.Copy(out, table)
	}
	return out, nil
}

// readLabels reads the labelling lanes' files: ID, label, ... separated by tabs. An ID that two files label differently keeps the
// first label that is not MISSING-DATA, so one doubt withholds the row.
func readLabels(spec string) (map[string]string, error) {
	out := map[string]string{}
	if spec == "" {
		return out, nil
	}
	for pattern := range strings.SplitSeq(spec, ",") {
		files, err := filepath.Glob(strings.TrimSpace(pattern))
		if err != nil {
			return nil, err
		}
		sort.Strings(files)
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			for line := range strings.SplitSeq(string(raw), "\n") {
				c := strings.Split(line, "\t")
				if len(c) < 2 {
					continue
				}
				if old, ok := out[c[0]]; !ok || old == "MISSING-DATA" {
					out[c[0]] = c[1]
				}
			}
		}
	}
	return out, nil
}

// readWiring reads the wiring evidence: ID, production file, note.
func readWiring(path string) (map[string]string, error) {
	out := map[string]string{}
	if path == "" {
		return out, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if c := strings.Split(line, "\t"); len(c) >= 2 && c[0] != "" {
			out[c[0]] = c[1]
		}
	}
	return out, nil
}

// verifyWiring refuses a field with no consumer: the named file must be production Go source that selects or sets the field.
func verifyWiring(root, file, field, owner string) string {
	if file == "" {
		return "no wiring site: the field would be set or read by no production code (list its ID in -wiring with the file that consumes it)"
	}
	if strings.HasSuffix(file, "_test.go") || !strings.HasSuffix(file, ".go") {
		return "wiring site " + file + " is not production Go source"
	}
	raw, err := os.ReadFile(under(root, file))
	if err != nil {
		return "wiring site " + file + " is unreadable: " + err.Error()
	}
	uses := regexp.MustCompile(`\.` + field + `\b|\b` + field + `\s*:`)
	for line := range strings.SplitSeq(string(raw), "\n") {
		if uses.MatchString(line) && !strings.HasPrefix(strings.TrimSpace(line), "//") {
			return ""
		}
	}
	return "wiring site " + file + " never selects or sets ." + field + " (" + owner + ")"
}

var declRe = regexp.MustCompile(`^\s*export\s+(?:declare\s+)?(?:abstract\s+)?(?:interface|type|class)\s+(\w+)`)

// pinnedCitation finds Parent's declaration in the pinned upstream TypeScript source and returns package/src/file:line of the property.
func pinnedCitation(upstreamDir, parent, prop string) (string, string) {
	var found string
	_ = filepath.WalkDir(upstreamDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" || d.IsDir() && (d.Name() == "node_modules" || d.Name() == "dist" || d.Name() == "test") || d.IsDir() || !strings.HasSuffix(path, ".ts") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			if m := declRe.FindStringSubmatch(line); m != nil && m[1] == parent {
				propRe := regexp.MustCompile(`^\s*(?:readonly\s+)?` + regexp.QuoteMeta(prop) + `\??\s*[:(]`)
				for j := i; j < len(lines) && j < i+400; j++ {
					if propRe.MatchString(lines[j]) {
						rel, _ := filepath.Rel(upstreamDir, path)
						found = "packages/" + filepath.ToSlash(rel) + ":" + strconv.Itoa(j+1)
						return nil
					}
					if j > i && strings.HasPrefix(lines[j], "}") {
						break
					}
				}
			}
		}
		return nil
	})
	if found == "" {
		return "", "no declaration of " + parent + "." + prop + " in the pinned upstream source " + upstreamDir + " to cite"
	}
	return found, ""
}

// under resolves a path against the repository root unless it is absolute.
func under(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

func readGaps(path string) ([]gap, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only
	var out []gap
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		c := strings.Split(sc.Text(), "\t")
		if len(c) >= 4 && c[2] == "member-missing" {
			out = append(out, gap{c[0], c[2], c[3]})
		}
	}
	return out, sc.Err()
}

func readInventory(path string) (map[string]*entry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Interfaces []*entry `json:"interfaces"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	by := make(map[string]*entry, len(doc.Interfaces))
	for _, e := range doc.Interfaces {
		by[e.ID] = e
	}
	return by, nil
}

// insertion is one field to add to a struct.
type insertion struct {
	owner   string // Go type name
	id      string
	goName  string
	goType  string
	tag     string
	comment string
}

type skip struct{ id, why string }

func run(o options) error {
	if o.upstream == "" {
		o.upstream = ".upstream/current/packages"
	}
	gaps, err := readGaps(under(o.root, o.gaps))
	if err != nil {
		return err
	}
	inv, err := readInventory(under(o.root, o.inventory))
	if err != nil {
		return err
	}
	renames, err := documentedRenames(o.root)
	if err != nil {
		return err
	}
	labels, err := readLabels(o.labels)
	if err != nil {
		return err
	}
	wiringPath := o.wiring
	if wiringPath != "" {
		wiringPath = under(o.root, wiringPath)
	}
	wires, err := readWiring(wiringPath)
	if err != nil {
		return err
	}
	byFile := map[string][]gap{}
	var skips []skip
	for _, g := range gaps {
		e := inv[g.id]
		if e == nil || e.Kind != "property" || strings.Contains(g.id, "::call:") {
			continue
		}
		if o.labels != "" {
			if label := labels[g.id]; label != "MISSING-DATA" {
				if label == "" {
					label = "none"
				}
				skips = append(skips, skip{g.id, "label " + label + ", not MISSING-DATA"})
				continue
			}
		}
		if target, ok := renames[g.id]; ok {
			// A documented rename says the Go member exists under another name; a second field would duplicate it.
			skips = append(skips, skip{g.id, "documented rename to " + target + " does not resolve to a member: fix the rename"})
			continue
		}
		parent := inv[e.ParentID]
		switch {
		case parent == nil || parent.Kind != "interface":
			continue // a class member is behaviour or an accessor; a person decides
		case g.target == "":
			skips = append(skips, skip{g.id, "no Go owner type exists"})
			continue
		}
		file, _, _ := strings.Cut(g.target, "#")
		byFile[file] = append(byFile[file], g)
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	dirs := map[string]bool{}
	for _, f := range files {
		dirs["./"+filepath.ToSlash(filepath.Dir(f))] = true
	}
	var patterns []string
	for d := range dirs {
		patterns = append(patterns, d)
	}
	sort.Strings(patterns)
	loaded, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps | packages.NeedModule,
		Dir:  o.root, Fset: token.NewFileSet(),
	}, patterns...)
	if err != nil {
		return err
	}
	pkgByDir := map[string]*packages.Package{}
	for _, p := range loaded {
		if p.Module != nil {
			dir := strings.TrimPrefix(strings.TrimPrefix(p.PkgPath, p.Module.Path), "/")
			if dir == "" {
				dir = "."
			}
			pkgByDir[dir] = p
		}
	}

	var added []insertion
	edits := map[string][]edit{}
	for _, file := range files {
		dir := filepath.ToSlash(filepath.Dir(file))
		pkg := pkgByDir[dir]
		if pkg == nil || pkg.Types == nil {
			for _, g := range byFile[file] {
				skips = append(skips, skip{g.id, "package " + dir + " did not load"})
			}
			continue
		}
		perOwner := map[string][]gap{}
		for _, g := range byFile[file] {
			_, owner, _ := strings.Cut(g.target, "#")
			perOwner[owner] = append(perOwner[owner], g)
		}
		owners := make([]string, 0, len(perOwner))
		for ow := range perOwner {
			owners = append(owners, ow)
		}
		sort.Strings(owners)
		for _, owner := range owners {
			tn, _ := pkg.Types.Scope().Lookup(owner).(*types.TypeName)
			if tn == nil {
				for _, g := range perOwner[owner] {
					skips = append(skips, skip{g.id, owner + " is not a type of " + dir})
				}
				continue
			}
			st, ok := tn.Type().Underlying().(*types.Struct)
			if !ok {
				for _, g := range perOwner[owner] {
					skips = append(skips, skip{g.id, owner + " is not a struct"})
				}
				continue
			}
			taken := map[string]bool{}
			for _, g := range perOwner[owner] {
				e := inv[g.id]
				ins, why := fieldFor(inv, e, inv[e.ParentID], pkg.Types, tn, st, taken)
				if why != "" {
					skips = append(skips, skip{g.id, why})
					continue
				}
				cite, why := pinnedCitation(under(o.root, o.upstream), inv[e.ParentID].Name, e.Shape.Name)
				if why != "" {
					skips = append(skips, skip{g.id, why})
					continue
				}
				ins.comment = fmt.Sprintf("%s is Pi %s.%s (%s).", ins.goName, inv[e.ParentID].Name, e.Shape.Name, cite)
				if o.apply {
					if why := verifyWiring(o.root, wires[g.id], ins.goName, owner); why != "" {
						skips = append(skips, skip{g.id, why})
						continue
					}
				}
				pos, fileName, err := closingBrace(pkg, tn)
				if err != nil {
					skips = append(skips, skip{g.id, owner + " is an alias or a one-line struct: the field cannot be inserted without reformatting it"})
					continue
				}
				added = append(added, ins)
				taken[ins.goName] = true
				if o.apply {
					edits[fileName] = append(edits[fileName], edit{offset: pos, text: ins.render()})
				}
			}
		}
	}
	if o.apply {
		for f, es := range edits {
			if err := applyEdits(f, es); err != nil {
				return err
			}
		}
	}
	return writeReport(o, added, skips)
}

func (in insertion) render() string {
	return fmt.Sprintf("\t// %s\n\t%s %s `json:\"%s\"`\n", in.comment, in.goName, in.goType, in.tag)
}

type edit struct {
	offset int
	text   string
}

// closingBrace returns the file offset of the closing brace of the struct that declares tn.
func closingBrace(pkg *packages.Package, tn *types.TypeName) (int, string, error) {
	for _, f := range pkg.Syntax {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, sp := range gd.Specs {
				ts, ok := sp.(*ast.TypeSpec)
				if !ok || ts.Name.Name != tn.Name() {
					continue
				}
				if s, ok := ts.Type.(*ast.StructType); ok {
					p := pkg.Fset.Position(s.Fields.Closing)
					if p.Line == pkg.Fset.Position(s.Fields.Opening).Line {
						return 0, "", fmt.Errorf("%s is declared on one line", tn.Name())
					}
					return p.Offset, p.Filename, nil
				}
			}
		}
	}
	return 0, "", fmt.Errorf("no struct declaration of %s", tn.Name())
}

func applyEdits(file string, es []edit) error {
	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	// One struct gets all its fields in the order they were generated: merge the edits that share an offset, then insert from the end.
	var merged []edit
	for _, e := range es {
		if n := len(merged); n > 0 && merged[n-1].offset == e.offset {
			merged[n-1].text += e.text
			continue
		}
		merged = append(merged, e)
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].offset > merged[j].offset })
	for _, e := range merged {
		src = append(src[:e.offset:e.offset], append([]byte(e.text), src[e.offset:]...)...)
	}
	out, err := format.Source(src)
	if err != nil {
		return fmt.Errorf("%s: generated source does not format: %w", file, err)
	}
	return os.WriteFile(file, out, 0o644)
}

func writeReport(o options, added []insertion, skips []skip) (err error) {
	var out io.Writer = os.Stdout
	if o.report != "" {
		f, err := os.Create(o.report)
		if err != nil {
			return err
		}
		defer func() {
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}()
		out = f
	}
	verb := "would add"
	if o.apply {
		verb = "added"
	}
	sort.Slice(added, func(i, j int) bool { return added[i].id < added[j].id })
	sort.Slice(skips, func(i, j int) bool { return skips[i].id < skips[j].id })
	var b strings.Builder
	fmt.Fprintf(&b, "# stubfields: %s %d field(s); %d left for a person\n", verb, len(added), len(skips))
	for _, a := range added {
		fmt.Fprintf(&b, "ADD\t%s\t%s.%s %s\n", a.id, a.owner, a.goName, a.goType)
	}
	for _, s := range skips {
		fmt.Fprintf(&b, "SKIP\t%s\t%s\n", s.id, s.why)
	}
	_, err = io.WriteString(out, b.String())
	return err
}

var acronyms = map[string]string{"Id": "ID", "Url": "URL", "Uri": "URI", "Json": "JSON", "Http": "HTTP", "Https": "HTTPS", "Api": "API", "Html": "HTML", "Sql": "SQL", "Tls": "TLS", "Ip": "IP", "Ui": "UI", "Cwd": "CWD", "Llm": "LLM", "Mcp": "MCP", "Oauth": "OAuth"}

var wordRe = regexp.MustCompile(`[A-Z]+[a-z0-9]*|[a-z0-9]+`)

// goName exports a camelCase upstream name and writes its initialisms in capitals (apiKey is APIKey, sessionId is SessionID).
func goName(up string) string {
	if up == "" {
		return up
	}
	r := []rune(up)
	r[0] = unicode.ToUpper(r[0])
	var b strings.Builder
	for _, w := range wordRe.FindAllString(string(r), -1) {
		if a, ok := acronyms[w]; ok {
			w = a
		}
		b.WriteString(w)
	}
	return b.String()
}
