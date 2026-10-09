package main

import (
	"go/token"
	"go/types"
	"reflect"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/test/parity/interface-closure/autobind/rules"
)

// sym is a Go declaration that may stand for an upstream one.
type sym struct {
	Kind  string // func, type, const, var, method, field
	Name  string
	Owner string // declaring type of a method or field
	JSON  string // json tag name of a field
	Dir   string
	File  string
	Obj   types.Object `json:"-"`
	Key   string
	Tier  string
	Rank  int // position of Dir in the package's directory seeds
}

func (s *sym) Qualified() string {
	if s.Owner != "" {
		return s.Owner + "." + s.Name
	}
	return s.Name
}

func (s *sym) Target() string { return s.File + "#" + s.Qualified() }

func (s *sym) Exported() bool {
	return token.IsExported(s.Name) && (s.Owner == "" || token.IsExported(s.Owner))
}

// PublicAPI reports whether the symbol is an exported declaration of a package outside internal/ and cmd/.
func (s *sym) PublicAPI() bool {
	if !s.Exported() {
		return false
	}
	for seg := range strings.SplitSeq(s.Dir, "/") {
		if seg == "internal" || seg == "cmd" {
			return false
		}
	}
	return true
}

// dirSeeds lists, for each upstream package, the repository directories that may hold its Go counterparts, best
// first. A trailing /... includes every descendant directory.
var dirSeeds = map[string][]string{
	"agent":                       {"agent"},
	"ai":                          {"ai"},
	"codemode":                    {"codemode", "coding/extension/builtin/codemode"},
	"env":                         {"env"},
	"client":                      {"internal/experimental/client"},
	"client/unix":                 {"internal/experimental/client"},
	"protocol":                    {"internal/experimental/protocol"},
	"server":                      {"internal/experimental/routing"},
	"server/testing":              {"internal/experimental/routing/routingtest"},
	"server/unix":                 {"internal/experimental/routing"},
	"telemetry":                   {"telemetry"},
	"telemetry/testing":           {"telemetry/telemetrytest"},
	"chord":                       {"internal/chord", "chord"},
	"chord/delta":                 {"chord/delta", "internal/chord/delta"},
	"chord/context":               {"internal/chord/chordctx"},
	"chord/bundler":               {"internal/experimental"},
	"chord/node":                  {"internal/experimental"},
	"codemode/declarations":       {"codemode"},
	"codemode/source":             {"codemode"},
	"durable":                     {"durable", "durable/harness", "durable/session"},
	"durable/env":                 {"durable/env"},
	"durable/env/node":            {"durable/env/node"},
	"durable/storage/jsonl":       {"durable/storage/jsonl"},
	"durable/storage/jsonl/node":  {"durable/storage/jsonl/node"},
	"durable/storage/memory":      {"durable/storage"},
	"durable/storage/sqlite":      {"durable/storage/sqlite"},
	"durable/storage/sqlite/node": {"durable/storage/sqlite/node"},
	"durable/testing":             {"durable/durabletest"},
	"durable/tools":               {"durable/tools"},
	"mcp":                         {"mcp", "mcp/oauth", "mcp/mcptest", "coding/mcpext"},
	"tui":                         {"tui", "tui/widthx", "internal/imageprocessing"},
	"coding-agent": {"coding", "internal/codingagent/...", "coding/extension/...", "coding/rpcclient", "internal/packagemanager",
		"internal/imageprocessing", "tui", "agent", "coding/cli", "coding/mcpext"},
}

// dirsFor returns the indexed directories for an upstream package, in seed order.
func (ix *index) dirsFor(pkg string) []string {
	return ix.expandDirs(append(append([]string{}, dirSeeds[pkg]...), rules.Dirs(pkg)...))
}

// shapeDirsFor is dirsFor restricted to the directories where a shape-only match may stand for an upstream symbol (rules.ShapeDirs).
func (ix *index) shapeDirsFor(pkg string) []string {
	return ix.expandDirs(append(append([]string{}, dirSeeds[pkg]...), rules.ShapeDirs(pkg)...))
}

func (ix *index) expandDirs(seeds []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, seed := range seeds {
		prefix, tree := strings.CutSuffix(seed, "/...")
		var matches []string
		for d := range ix.pkgs {
			if d == prefix || tree && strings.HasPrefix(d, prefix+"/") {
				matches = append(matches, d)
			}
		}
		sort.Strings(matches)
		for _, d := range matches {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}

func (ix *index) newSym(kind string, obj types.Object, dir string, rank int) *sym {
	s := &sym{Kind: kind, Name: obj.Name(), Dir: dir, File: ix.file(obj), Obj: obj, Key: ix.declKey(obj), Rank: rank}
	return s
}

// topLevel lists the package-level declarations of the directories, in seed order, declared in non-test files.
func (ix *index) topLevel(dirs []string) []*sym {
	var out []*sym
	for rank, d := range dirs {
		scope := ix.pkgs[d].Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if isTestFile(ix.pos(obj.Pos()).Filename) {
				continue
			}
			var kind string
			switch obj.(type) {
			case *types.Func:
				kind = "func"
			case *types.TypeName:
				kind = "type"
			case *types.Const:
				kind = "const"
			case *types.Var:
				kind = "var"
			default:
				continue
			}
			out = append(out, ix.newSym(kind, obj, d, rank))
		}
	}
	return out
}

// ownerOf names the type that declares a method or field.
func (ix *index) ownerOf(obj types.Object, fallback string) string {
	if o, ok := ix.owners[ix.declKey(obj)]; ok {
		return o
	}
	if fn, ok := obj.(*types.Func); ok {
		if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
			if n := namedOf(deref(sig.Recv().Type())); n != nil {
				return n.Obj().Name()
			}
		}
	}
	return fallback
}

// members lists the fields and methods of a Go type, including those promoted from embedded types.
func (ix *index) members(tn *types.TypeName, dir string, rank int) []*sym {
	t := tn.Type()
	var out []*sym
	seen := map[string]bool{}
	add := func(s *sym) {
		if !seen[s.Key] {
			seen[s.Key] = true
			out = append(out, s)
		}
	}
	var ms *types.MethodSet
	if types.IsInterface(t) {
		ms = types.NewMethodSet(t)
	} else {
		ms = types.NewMethodSet(types.NewPointer(t))
	}
	for method := range ms.Methods() {
		fn, ok := method.Obj().(*types.Func)
		if !ok || fn.Pkg() == nil || !ix.inModule(fn.Pkg().Path()) || isTestFile(ix.pos(fn.Pos()).Filename) {
			continue
		}
		s := ix.newSym("method", fn, ix.dirOf(fn), rank)
		s.Owner = ix.ownerOf(fn, tn.Name())
		add(s)
	}
	var walk func(st *types.Struct, depth int)
	walk = func(st *types.Struct, depth int) {
		for i := 0; i < st.NumFields(); i++ {
			f := st.Field(i)
			if f.Embedded() {
				if depth < 3 {
					if es, ok := deref(f.Type()).Underlying().(*types.Struct); ok {
						walk(es, depth+1)
					}
				}
				continue
			}
			if f.Pkg() == nil || !ix.inModule(f.Pkg().Path()) || isTestFile(ix.pos(f.Pos()).Filename) {
				continue
			}
			s := ix.newSym("field", f, ix.dirOf(f), rank)
			s.Owner = ix.ownerOf(f, tn.Name())
			if tag := reflect.StructTag(st.Tag(i)).Get("json"); tag != "" {
				name, _, _ := strings.Cut(tag, ",")
				if name != "-" {
					s.JSON = name
				}
			}
			add(s)
		}
	}
	if st, ok := t.Underlying().(*types.Struct); ok {
		walk(st, 0)
	}
	return out
}

func (ix *index) dirOf(obj types.Object) string {
	d := ix.file(obj)
	if i := strings.LastIndex(d, "/"); i >= 0 {
		return d[:i]
	}
	return "."
}

// typeKeys returns the declaration keys whose references count as uses of a type: the type and its own fields.
func (ix *index) typeKeys(tn *types.TypeName) []string {
	keys := []string{ix.declKey(tn)}
	if st, ok := tn.Type().Underlying().(*types.Struct); ok {
		for f := range st.Fields() {
			if !f.Embedded() {
				keys = append(keys, ix.declKey(f))
			}
		}
	}
	return keys
}
