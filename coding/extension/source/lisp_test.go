package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// withLisp runs a test as a program that never called EnableLisp, or as one that did, and restores the hooks and the factory pattern.
func withLisp(t *testing.T, enabled bool) {
	t.Helper()
	savedLisp, savedFactory := lisp, lispFactory
	t.Cleanup(func() { lisp, lispFactory = savedLisp, savedFactory })
	lisp.root, lisp.file, lispFactory = nil, nil, (*lazyregexp.Regexp)(nil)
	if enabled {
		EnableLisp()
	}
}

// Lisp extension sources resolve only in a program that called EnableLisp; any other program reports the language as unsupported.
func TestLispSourcesResolveOnlyAfterEnableLisp(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("greeter.asd", `(asdf:defsystem "greeter" :components ((:file "greeter")))`)
	file := write("greeter.lisp", "(defun new-extension () nil)\n")

	withLisp(t, false)
	if _, err := Resolve(root); err == nil || !strings.Contains(err.Error(), `unsupported language "lisp"`) {
		t.Errorf("Resolve(root) without EnableLisp error = %v, want an unsupported lisp language", err)
	}
	if _, err := Resolve(file); err == nil {
		t.Errorf("Resolve(file) without EnableLisp resolved a non-executable .lisp file")
	}

	withLisp(t, true)
	definition, err := Resolve(root)
	if err != nil || definition.Language != "lisp" || definition.Form != Factory || definition.Package != "greeter" || definition.Factory != "new-extension" || !definition.Packable {
		t.Errorf("Resolve(root) = %+v, %v, want the packable lisp factory of system greeter", definition, err)
	}
	definition, err = Resolve(file)
	if err != nil || definition.Language != "lisp" || definition.Form != Factory || definition.Package != "greeter" {
		t.Errorf("Resolve(file) = %+v, %v, want the lisp factory greeter", definition, err)
	}
}
