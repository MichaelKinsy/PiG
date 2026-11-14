package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A component that embeds a Container renders by it: a parent container reaches the component through Render and, before it renders, through
// the promoted IsDirty, whose scan renders the component's own children (container_settled.go). When the component guards, with its own mutex,
// state that those children read, and a goroutine other than the loop writes that state (an OAuth flow's callbacks, a clipboard writer), an
// entry point that skips the lock reads it mid-write: LoginDialogComponent's settle scan read d.input and d.secrets while ShowPrompt wrote them.
// The guard reads every non-test Go source of the repository and fails for a struct type that embeds a Container (directly or through another
// such type) and has a sync mutex field, unless Render and IsDirty are declared on it and each takes a mutex of the type.

const guardTUIImport = "github.com/MichaelKinsy/PiG/tui"

// lockedContainerExempt names, with the reason, a type that embeds a Container and has a mutex that guards nothing its children render.
// An entry that is no longer such a type fails the guard.
var lockedContainerExempt = map[string]string{
	"tui.AuthURL":    "its mutex guards the copy outcome, which the hint child reads through texts() under that mutex on every render; the promoted Render and IsDirty reach no other guarded state (TestAuthURLCopyOutcomeRendersRaceFreeThroughAParent)",
	"tui.ScrollView": "its mutex guards the scroll and scrollbar state, which Render reads through GetContentWidth under that mutex; the promoted IsDirty scans only the wrapped child, which reads none of it",
	"tui.tuiBase":    "the renderer both TUI types embed is the root of the frame, not a child a parent container renders or asks IsDirty; its overlayMu guards the overlay stack, which no Container child reads",
}

type guardFile struct {
	dir  string
	file *ast.File
}

type guardStruct struct {
	embeds  []string
	mutexes []string
}

// embeddedRef returns "dir.Type" of an embedded field: a type of the same package, or one of the tui package through its import name.
func embeddedRef(f *ast.Field, file *ast.File, dir, tuiDir string) (string, bool) {
	if len(f.Names) != 0 {
		return "", false
	}
	expr := f.Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return dir + "." + e.Name, true
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		for _, imp := range file.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); path != guardTUIImport {
				continue
			}
			local := "tui"
			if imp.Name != nil {
				local = imp.Name.Name
			}
			if x.Name == local {
				return tuiDir + "." + e.Sel.Name, true
			}
		}
	}
	return "", false
}

// mutexFields returns the names of the sync.Mutex or sync.RWMutex a field declares; an embedded one is named by its type.
func mutexFields(f *ast.Field) []string {
	sel, ok := f.Type.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok || x.Name != "sync" || (sel.Sel.Name != "Mutex" && sel.Sel.Name != "RWMutex") {
		return nil
	}
	if len(f.Names) == 0 {
		return []string{sel.Sel.Name}
	}
	names := make([]string, 0, len(f.Names))
	for _, n := range f.Names {
		names = append(names, n.Name)
	}
	return names
}

// takesLock reports that fn calls Lock or RLock on one of the type's mutexes.
func takesLock(fn *ast.FuncDecl, mutexes []string) bool {
	if fn.Body == nil {
		return false
	}
	recv := ""
	if names := fn.Recv.List[0].Names; len(names) > 0 {
		recv = names[0].Name
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "Lock" && sel.Sel.Name != "RLock") {
			return true
		}
		switch x := sel.X.(type) {
		case *ast.SelectorExpr:
			found = found || slices.Contains(mutexes, x.Sel.Name)
		case *ast.Ident:
			found = found || (x.Name == recv && (slices.Contains(mutexes, "Mutex") || slices.Contains(mutexes, "RWMutex")))
		}
		return true
	})
	return found
}

// lockedContainerTypes returns every struct type that embeds a Container, directly or through another such type, and has a sync mutex field
// (keyed "dir.Type", with the mutex field names), and one line per entry point of those that is not declared or does not take the lock.
func lockedContainerTypes(files []guardFile, tuiDir string) (flagged map[string][]string, offenders []string) {
	structs := map[string]guardStruct{}
	methods := map[string]*ast.FuncDecl{}
	for _, gf := range files {
		for _, decl := range gf.file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					var gs guardStruct
					for _, f := range st.Fields.List {
						if ref, ok := embeddedRef(f, gf.file, gf.dir, tuiDir); ok {
							gs.embeds = append(gs.embeds, ref)
						}
						gs.mutexes = append(gs.mutexes, mutexFields(f)...)
					}
					structs[gf.dir+"."+ts.Name.Name] = gs
				}
			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) != 1 {
					continue
				}
				recv := d.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				if id, ok := recv.(*ast.Ident); ok {
					methods[gf.dir+"."+id.Name+"."+d.Name.Name] = d
				}
			}
		}
	}
	base := tuiDir + ".Container"
	container := map[string]bool{base: true}
	for changed := true; changed; {
		changed = false
		for key, gs := range structs {
			if !container[key] && slices.ContainsFunc(gs.embeds, func(ref string) bool { return container[ref] }) {
				container[key], changed = true, true
			}
		}
	}
	flagged = map[string][]string{}
	for key := range container {
		gs := structs[key]
		if key == base || len(gs.mutexes) == 0 {
			continue
		}
		flagged[key] = gs.mutexes
		for _, name := range []string{"Render", "IsDirty"} {
			if fn := methods[key+"."+name]; fn == nil || !takesLock(fn, gs.mutexes) {
				offenders = append(offenders, key+" does not take its lock in "+name)
			}
		}
	}
	sort.Strings(offenders)
	return flagged, offenders
}

// repoGuardFiles parses the non-test Go files of every directory of the repository that mentions a Container and a sync mutex.
func repoGuardFiles(t *testing.T) []guardFile {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	byDir := map[string][]string{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "third_party" || name == "tmp" || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			byDir[filepath.Dir(path)] = append(byDir[filepath.Dir(path)], path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var files []guardFile
	fset := token.NewFileSet()
	for dir, paths := range byDir {
		sources := make([][]byte, len(paths))
		container, mutex := false, false
		for i, path := range paths {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			sources[i] = src
			text := string(src)
			container = container || strings.Contains(text, "Container")
			mutex = mutex || strings.Contains(text, "sync.Mutex") || strings.Contains(text, "sync.RWMutex")
		}
		if !container || !mutex {
			continue
		}
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			t.Fatal(err)
		}
		for i, path := range paths {
			file, err := parser.ParseFile(fset, path, sources[i], 0)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, guardFile{dir: filepath.ToSlash(rel), file: file})
		}
	}
	return files
}

func TestEveryLockedContainerComponentTakesItsLockInRenderAndIsDirty(t *testing.T) {
	flagged, offenders := lockedContainerTypes(repoGuardFiles(t), "tui")
	keys := make([]string, 0, len(flagged))
	for key := range flagged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	t.Logf("types that embed a Container and have a mutex: %v", keys)
	offending := map[string]bool{}
	var failures []string
	for _, line := range offenders {
		key, _, _ := strings.Cut(line, " ")
		offending[key] = true
		if _, exempt := lockedContainerExempt[key]; !exempt {
			failures = append(failures, line+": the promoted method reads the children without the lock")
		}
	}
	for key, reason := range lockedContainerExempt {
		if !offending[key] {
			failures = append(failures, "exempt "+key+" ("+reason+") is no longer a Container-embedding type with a mutex that skips its lock: drop the entry")
		}
	}
	sort.Strings(failures)
	if len(failures) > 0 {
		t.Fatalf("%s", strings.Join(failures, "\n"))
	}
}

const lockedContainerFixture = `package x
import "sync"
type Container struct{ mu sync.Mutex }
type Good struct { Container; mu sync.Mutex }
func (g *Good) Render(int) []string { g.mu.Lock(); defer g.mu.Unlock(); return nil }
func (g *Good) IsDirty() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.Container.IsDirty() }
type NoDirty struct { *Container; mu sync.RWMutex }
func (g *NoDirty) Render(int) []string { g.mu.RLock(); defer g.mu.RUnlock(); return nil }
type NoLock struct { Container; mu sync.Mutex }
func (g *NoLock) Render(int) []string { g.mu.Lock(); return nil }
func (g *NoLock) IsDirty() bool { return g.Container.IsDirty() }
type Plain struct { Container }
type Via struct { NoDirty; sync.Mutex }
`

func TestTheLockedContainerGuardFailsOnAMissingOrUnlockedOverride(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "x.go", lockedContainerFixture, 0)
	if err != nil {
		t.Fatal(err)
	}
	flagged, offenders := lockedContainerTypes([]guardFile{{dir: "x", file: file}}, "x")
	var got []string
	for key := range flagged {
		got = append(got, key)
	}
	sort.Strings(got)
	if want := []string{"x.Good", "x.NoDirty", "x.NoLock", "x.Via"}; !slices.Equal(got, want) {
		t.Fatalf("flagged %v, want %v", got, want)
	}
	want := []string{
		"x.NoDirty does not take its lock in IsDirty",
		"x.NoLock does not take its lock in IsDirty",
		"x.Via does not take its lock in IsDirty",
		"x.Via does not take its lock in Render",
	}
	if !slices.Equal(offenders, want) {
		t.Fatalf("offenders %q, want %q", offenders, want)
	}
}
