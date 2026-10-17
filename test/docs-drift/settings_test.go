package docsdrift

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The settings reference lists the keys of settings.json. Each key it names must be a key PiG's Settings type reads, and each key the type reads must be in the reference or on the list of keys that are deliberately not documented.

var settingsRow = regexp.MustCompile("(?m)^\\| `([A-Za-z][A-Za-z0-9.]*)` \\|")

// settingsKeys reads the members of settings.json from the declarations of internal/codingagent/settings.go: the struct settingsWire is what PiG decodes, and the structs its members name give the nested keys. Reading the source keeps the list from drifting from the type.
type settingsKeys struct {
	structs map[string]*ast.StructType
}

func loadSettingsKeys(t *testing.T) settingsKeys {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(repoRoot, "internal", "codingagent", "settings.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	k := settingsKeys{structs: map[string]*ast.StructType{}}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok {
				if st, ok := ts.Type.(*ast.StructType); ok {
					k.structs[ts.Name.Name] = st
				}
			}
		}
	}
	if k.structs["settingsWire"] == nil {
		t.Fatal("internal/codingagent/settings.go no longer declares settingsWire; update the docs-drift settings reader")
	}
	return k
}

// members returns the JSON member names of a struct and the name of the struct type each member holds, when it holds one this file declares.
func (k settingsKeys) members(structName string) map[string]string {
	out := map[string]string{}
	st := k.structs[structName]
	if st == nil {
		return out
	}
	for _, f := range st.Fields.List {
		if f.Tag == nil {
			continue
		}
		tag := reflect.StructTag(strings.Trim(f.Tag.Value, "`")).Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		out[name] = typeName(f.Type)
	}
	return out
}

func typeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return typeName(x.X)
	case *ast.Ident:
		return x.Name
	}
	return ""
}

// has reports whether the dotted path names a member.
func (k settingsKeys) has(path []string) bool {
	cur := "settingsWire"
	for i, part := range path {
		typ, ok := k.members(cur)[part]
		if !ok {
			return false
		}
		if i < len(path)-1 {
			if k.structs[typ] == nil {
				return false
			}
			cur = typ
		}
	}
	return true
}

// undocumentedSettings are keys Settings carries that the reference leaves out, each with the reason.
var undocumentedSettings = map[string]string{}

func settingsPages(t *testing.T) []docSource {
	t.Helper()
	var out []docSource
	for _, src := range corpus(t) {
		switch src.Rel {
		case "docs/site/docs/settings.md", "internal/pigdocs/content/settings.md":
			out = append(out, src)
		}
	}
	if len(out) != 2 {
		t.Fatalf("a settings reference is missing from the corpus: found %d of 2", len(out))
	}
	return out
}

func TestDocumentedSettingsKeysExist(t *testing.T) {
	keys := loadSettingsKeys(t)
	if n := len(keys.members("settingsWire")); n < 40 {
		t.Fatalf("found only %d settings keys in settingsWire; the reader is broken", n)
	}
	for _, page := range settingsPages(t) {
		rows := settingsRow.FindAllStringSubmatch(page.Body, -1)
		if len(rows) < 50 {
			t.Fatalf("found only %d settings rows in %s", len(rows), page.Rel)
		}
		for _, m := range rows {
			if !keys.has(strings.Split(m[1], ".")) {
				t.Errorf("%s documents the setting %q, which PiG does not read (settingsWire in internal/codingagent/settings.go)", page.Rel, m[1])
			}
		}
	}
}

func TestEverySettingsKeyIsDocumented(t *testing.T) {
	top := loadSettingsKeys(t).members("settingsWire")
	for _, page := range settingsPages(t) {
		documented := map[string]bool{}
		for _, m := range settingsRow.FindAllStringSubmatch(page.Body, -1) {
			documented[strings.Split(m[1], ".")[0]] = true
		}
		for key := range top {
			if !documented[key] && undocumentedSettings[key] == "" {
				t.Errorf("%s does not document the setting %q, which PiG reads (settingsWire in internal/codingagent/settings.go)", page.Rel, key)
			}
		}
	}
}

// A JSON sample that is a settings.json file (its top-level keys include a setting PiG reads) must use only keys PiG reads, so an example cannot teach a key that does nothing.
func TestSettingsSamplesUseKnownKeys(t *testing.T) {
	keys := loadSettingsKeys(t)
	checked := 0
	bodies := map[string][]string{}
	for _, src := range corpus(t) {
		bodies[src.Rel] = strings.Split(src.Body, "\n")
	}
	for _, s := range allSamples(t) {
		if s.Lang != "json" && s.Lang != "jsonc" || s.Skip != "" {
			continue
		}
		// Only a sample the page introduces as settings.json: other JSON that shares key names, such as an RPC state reply, is not one.
		lines := bodies[s.Src]
		from := max(0, s.Line-9)
		if !strings.Contains(strings.Join(lines[from:s.Line-1], "\n"), "settings.json") {
			continue
		}
		var obj map[string]json.RawMessage
		body := elidedLine.ReplaceAllString(elidedArray.ReplaceAllString(elidedObject.ReplaceAllString(stripJSONC(s.Body), "{}"), "[]"), "")
		if json.Unmarshal([]byte(body), &obj) != nil {
			continue
		}
		known := 0
		for k := range obj {
			if len(keys.members("settingsWire")) > 0 && keys.has([]string{k}) {
				known++
			}
		}
		if known < 1 {
			continue
		}
		checked++
		for k, raw := range obj {
			if !keys.has([]string{k}) {
				t.Errorf("%s:%d: the settings sample uses %q, which PiG does not read", s.Src, s.Line, k)
				continue
			}
			var nested map[string]json.RawMessage
			if json.Unmarshal(raw, &nested) == nil {
				for n := range nested {
					if keys.members("settingsWire")[k] != "" && !keys.has([]string{k, n}) && keys.structs[keys.members("settingsWire")[k]] != nil {
						t.Errorf("%s:%d: the settings sample uses %q, which PiG does not read", s.Src, s.Line, k+"."+n)
					}
				}
			}
		}
	}
	if checked < 3 {
		t.Fatalf("recognized only %d settings samples; the recognizer is broken", checked)
	}
}
