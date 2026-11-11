package runtimecell

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// An extension whose .asd depends on a Quicklisp system builds into an image with that system's source fetched from the (fake) pinned dist.
func TestBuildLispCellResolvesQuicklispDependencies(t *testing.T) {
	sbcl := toolchain.SBCLExecutable(runtime.GOOS, exec.LookPath)
	if _, err := exec.LookPath(sbcl); err != nil {
		t.Skipf("%s not found: %v", sbcl, err)
	}
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk-lisp"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_SDK_LISP_ROOT", sdkRoot)
	archives := map[string][]byte{
		"greeter": tgz(t, map[string]string{
			"greeter-1/greeter.asd":  `(asdf:defsystem "greeter" :version "1.0" :depends-on ("sb-posix") :components ((:file "greeter")))`,
			"greeter-1/greeter.lisp": `(defpackage "GREETER" (:use "CL") (:export "NAME")) (in-package "GREETER") (defun name () "from-quicklisp")`,
		}),
	}
	d := installFakeDist(t, archives, "# project system-file system-name dep...\ngreeter greeter greeter sb-posix\n")
	root := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("qlext.asd", `(asdf:defsystem "qlext" :depends-on ("greeter" (:version "greeter" "0.5") (:feature :no-such-feature "absent")) :components ((:file "qlext")))`)
	write("qlext.lisp", `(defpackage "QLEXT" (:use "CL")) (in-package "QLEXT") (defun new-extension () (pig:make-extension (greeter:name)))`)
	extensions := []LispExtension{{Name: "qlext", Root: root, Package: "qlext", Factory: "new-extension", Hash: "h1"}}
	cache := t.TempDir()
	cell, err := BuildLispPackedCell(context.Background(), cache, "cell:ql", extensions)
	if err != nil {
		if _, after, ok := strings.Cut(err.Error(), "details: "); ok {
			if log, readErr := os.ReadFile(strings.TrimSuffix(after, ")")); readErr == nil {
				t.Logf("build log:\n%s", log)
			}
		}
		t.Fatal(err)
	}
	if cell.BinaryPath == "" || len(d.fetched) != 3 {
		t.Fatalf("cell = %+v, fetched %v (want the two index files and one archive)", cell, d.fetched)
	}
	t.Setenv("PIG_OFFLINE", "1")
	cached, err := BuildLispPackedCell(context.Background(), cache, "cell:ql", extensions)
	if err != nil || !cached.Cached {
		t.Fatalf("cached cell = %+v, %v", cached, err)
	}
	// a changed source with an empty source cache and no network stops the build
	write("qlext.lisp", `(defpackage "QLEXT" (:use "CL")) (in-package "QLEXT") (defun new-extension () (pig:make-extension (greeter:name))) ; changed`)
	extensions[0].Hash = "h2"
	if err := os.RemoveAll(filepath.Join(cache, "quicklisp")); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildLispPackedCell(context.Background(), cache, "cell:ql", extensions); err == nil || !strings.Contains(err.Error(), "PIG_OFFLINE") {
		t.Fatalf("offline build error = %v, want a PIG_OFFLINE failure", err)
	}
}

// A member whose directory is deep builds: ASDF's compile output used to mirror the source's absolute path under the scratch
// directory, which past MAX_PATH made SBCL on Windows exit with an access violation (0xc0000005) while loading the system.
func TestBuildLispCellWithDeepSourceDirectory(t *testing.T) {
	sbcl := toolchain.SBCLExecutable(runtime.GOOS, exec.LookPath)
	if _, err := exec.LookPath(sbcl); err != nil {
		t.Skipf("%s not found: %v", sbcl, err)
	}
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk-lisp"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_SDK_LISP_ROOT", sdkRoot)
	root := filepath.Join(t.TempDir(), strings.Repeat("a", 60), strings.Repeat("b", 60))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"deep.asd":  `(asdf:defsystem "deep" :components ((:file "deep")))`,
		"deep.lisp": `(defpackage "DEEP" (:use "CL")) (in-package "DEEP") (defun new-extension () (pig:make-extension "deep"))`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cache := filepath.Join(t.TempDir(), strings.Repeat("c", 40))
	cell, err := BuildLispPackedCell(context.Background(), cache, "cell:deep", []LispExtension{{Name: "deep", Root: root, Package: "deep", Factory: "new-extension", Hash: "h1"}})
	if err != nil {
		if _, after, ok := strings.Cut(err.Error(), "details: "); ok {
			if log, readErr := os.ReadFile(strings.TrimSuffix(after, ")")); readErr == nil {
				t.Logf("build log:\n%s", log)
			}
		}
		t.Fatal(err)
	}
	if cell.BinaryPath == "" {
		t.Fatalf("cell = %+v", cell)
	}
}
