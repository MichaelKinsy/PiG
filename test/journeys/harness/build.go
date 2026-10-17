package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Variant selects how the PiG binary under test is built.
type Variant string

// Build variants.
const (
	// Plain is the release build plus the request-validity tag when the tree has it.
	Plain Variant = "plain"
	// Instrumented adds the race detector and the lock-order wrappers.
	Instrumented Variant = "instrumented"
	// Cover adds coverage counters for every package of the module (journey coverage for test pruning).
	Cover Variant = "cover"
)

const lockorderImport = "github.com/MichaelKinsy/PiG/internal/lockorder"

// BuildPig builds ./cmd/pig of the module at moduleRoot into out, returning the build duration.
func BuildPig(moduleRoot, out string, v Variant) (time.Duration, error) {
	start := time.Now()
	moduleRoot, err := filepath.Abs(moduleRoot)
	if err != nil {
		return 0, err
	}
	args := []string{"build", "-trimpath", "-o", out}
	if _, err := os.Stat(filepath.Join(moduleRoot, "internal", "requestvalidity")); err == nil {
		args = append(args, "-tags", "requestvalidity")
	}
	switch v {
	case Instrumented:
		overlay, err := writeLockOverlay(moduleRoot, filepath.Dir(out))
		if err != nil {
			return 0, err
		}
		args = append(args, "-race", "-overlay", overlay)
	case Cover:
		args = append(args, "-cover", "-coverpkg=github.com/MichaelKinsy/PiG/...")
	}
	args = append(args, "./cmd/pig")
	cmd := exec.Command("go", args...)
	cmd.Dir = moduleRoot
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, buf.String())
	}
	return time.Since(start), nil
}

// writeLockOverlay rewrites sync.Mutex and sync.RWMutex to the lockorder wrappers in every non-test file of the module's own packages and returns the path of the overlay JSON for go build.
func writeLockOverlay(moduleRoot, dir string) (string, error) {
	gen := filepath.Join(dir, "lockorder-overlay")
	if err := os.MkdirAll(gen, 0o755); err != nil {
		return "", err
	}
	replace := map[string]string{}
	type srcFile struct{ path, rel string }
	byDir := map[string][]srcFile{}
	err := filepath.WalkDir(moduleRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(moduleRoot, path)
		if d.IsDir() {
			switch {
			case rel == ".":
				return nil
			case d.Name() == "node_modules" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") || rel == filepath.Join("internal", "lockorder") || rel == "test":
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir // another module
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		byDir[filepath.Dir(path)] = append(byDir[filepath.Dir(path)], srcFile{path, rel})
		return nil
	})
	if err != nil {
		return "", err
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	slices.Sort(dirs)
	for _, dir := range dirs {
		// A lock whose address is taken may be passed to code that names the stock type, so its declaration keeps the generic wrapper.
		addressed := map[string]bool{}
		sources := map[string][]byte{}
		for _, f := range byDir[dir] {
			src, err := os.ReadFile(f.path)
			if err != nil {
				return "", err
			}
			sources[f.path] = src
			collectAddressed(f.path, src, addressed)
		}
		for _, f := range byDir[dir] {
			src := sources[f.path]
			if !bytes.Contains(src, []byte("sync.")) {
				continue
			}
			out, changed, err := rewriteSync(f.path, f.rel, len(replace), src, addressed)
			if err != nil {
				return "", err
			}
			if !changed {
				continue
			}
			target := filepath.Join(gen, strconv.Itoa(len(replace))+".go")
			if err := os.WriteFile(target, out, 0o644); err != nil {
				return "", err
			}
			replace[f.path] = target
		}
	}
	data, _ := json.Marshal(map[string]any{"Replace": replace})
	overlay := filepath.Join(dir, "lockorder-overlay.json")
	return overlay, os.WriteFile(overlay, data, 0o644)
}

// rewriteSync replaces sync.Mutex and sync.RWMutex by byte offset, so line numbers (and so stack traces) never move. A struct field or variable declared with the type gets its own generated type that names the declaration as the lock class; every other use becomes the generic lockorder wrapper.
func rewriteSync(path, rel string, fileIndex int, src []byte, addressed map[string]bool) ([]byte, bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, false, err
	}
	local := ""
	var syncSpec *ast.ImportSpec
	for _, imp := range file.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == "sync" {
			syncSpec = imp
			local = "sync"
			if imp.Name != nil {
				local = imp.Name.Name
			}
		}
	}
	if syncSpec == nil || local == "_" || local == "." {
		return nil, false, nil
	}
	type edit struct {
		off     int
		del     int
		replace string
	}
	var edits []edit
	var generated strings.Builder
	typed := map[*ast.SelectorExpr]bool{}
	isLock := func(e ast.Expr) (*ast.SelectorExpr, bool) {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return nil, false
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name != local || id.Obj != nil {
			return nil, false
		}
		return sel, sel.Sel.Name == "Mutex" || sel.Sel.Name == "RWMutex"
	}
	declare := func(sel *ast.SelectorExpr) {
		typed[sel] = true
		pos := fset.Position(sel.Pos())
		name := fmt.Sprintf("lockorderT%d_%d", fileIndex, len(typed))
		site := fmt.Sprintf("%s:%d", rel, pos.Line)
		edits = append(edits, edit{off: pos.Offset, del: fset.Position(sel.End()).Offset - pos.Offset, replace: name})
		kind := sel.Sel.Name
		fmt.Fprintf(&generated, "\ntype %s struct{ lockorder.%s }\n", name, kind)
		fmt.Fprintf(&generated, "func (m *%s) Lock() { m.%s.LockAs(%q) }\n", name, kind, site)
		fmt.Fprintf(&generated, "func (m *%s) TryLock() bool { return m.%s.TryLockAs(%q) }\n", name, kind, site)
		fmt.Fprintf(&generated, "func (m *%s) Unlock() { m.%s.Unlock() }\n", name, kind)
		if kind == "RWMutex" {
			fmt.Fprintf(&generated, "func (m *%s) RLock() { m.RWMutex.RLockAs(%q) }\n", name, site)
			fmt.Fprintf(&generated, "func (m *%s) TryRLock() bool { return m.RWMutex.TryRLockAs(%q) }\n", name, site)
			fmt.Fprintf(&generated, "func (m *%s) RUnlock() { m.RWMutex.RUnlock() }\n", name)
			fmt.Fprintf(&generated, "func (m *%s) RLocker() sync.Locker { return (*lockorderR%d_%d)(m) }\n", name, fileIndex, len(typed))
			fmt.Fprintf(&generated, "type lockorderR%d_%d %s\n", fileIndex, len(typed), name)
			fmt.Fprintf(&generated, "func (r *lockorderR%d_%d) Lock() { (*%s)(r).RLock() }\n", fileIndex, len(typed), name)
			fmt.Fprintf(&generated, "func (r *lockorderR%d_%d) Unlock() { (*%s)(r).RUnlock() }\n", fileIndex, len(typed), name)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Field:
			if sel, ok := isLock(x.Type); ok && len(x.Names) > 0 && !addressed[x.Names[0].Name] {
				declare(sel)
			}
		case *ast.ValueSpec:
			if sel, ok := isLock(x.Type); ok && x.Type != nil && !addressed[x.Names[0].Name] {
				declare(sel)
			}
		}
		return true
	})
	other := 0
	generic := 0
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || typed[sel] {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name != local || id.Obj != nil {
			return true
		}
		if sel.Sel.Name == "Mutex" || sel.Sel.Name == "RWMutex" {
			edits = append(edits, edit{off: fset.Position(id.Pos()).Offset, del: len(local), replace: "lockorder"})
			generic++
		} else {
			other++
		}
		return true
	})
	if len(edits) == 0 {
		return nil, false, nil
	}
	if other == 0 && generated.Len() == 0 || other == 0 && !strings.Contains(generated.String(), "sync.Locker") {
		// "sync" would be unused: keep the import for its side effect only.
		if syncSpec.Name != nil {
			return nil, false, fmt.Errorf("%s: aliased sync import without other uses", path)
		}
		edits = append(edits, edit{off: fset.Position(syncSpec.Path.Pos()).Offset, replace: "_ "})
	}
	pkgEnd := fset.Position(file.Name.End()).Offset
	edits = append(edits, edit{off: pkgEnd, replace: "; import lockorder \"" + lockorderImport + "\""})
	slices.SortFunc(edits, func(a, b edit) int { return b.off - a.off })
	out := slices.Clone(src)
	for _, e := range edits {
		out = append(out[:e.off], append([]byte(e.replace), out[e.off+e.del:]...)...)
	}
	out = append(out, generated.String()...)
	return out, true, nil
}

// collectAddressed records the names of variables and fields that appear under & anywhere in src.
func collectAddressed(path string, src []byte, into map[string]bool) {
	file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	if err != nil {
		return
	}
	ast.Inspect(file, func(n ast.Node) bool {
		u, ok := n.(*ast.UnaryExpr)
		if !ok || u.Op != token.AND {
			return true
		}
		switch x := u.X.(type) {
		case *ast.SelectorExpr:
			into[x.Sel.Name] = true
		case *ast.Ident:
			into[x.Name] = true
		}
		return true
	})
}
