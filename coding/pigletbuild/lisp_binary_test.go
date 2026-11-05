package pigletbuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "github.com/MichaelKinsy/PiG/coding/extension/host/lispcell" // the build resolves and builds Lisp extensions, which the extension host alone cannot
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// writeLispPiglet writes a Piglet with a Lisp ASDF system: a tool the faux model calls (echo_bridge) and a command that writes the file its
// argument names. With fused it also lists an empty Go factory, the workaround that let a Lisp Piglet pass the check that a Binary embeds or
// fuses something while the Lisp cell was not embedded.
func writeLispPiglet(t *testing.T, root string, fused bool) string {
	t.Helper()
	members := "  - name: lispbin\n    origins: [local:./lispbin]\n"
	if fused {
		members += "  - name: fuse-anchor\n    origins: [local:./fuse-anchor]\n"
		writeFiles(t, map[string]string{
			filepath.Join(root, "fuse-anchor", "go.mod"):       "module example.com/lispbin/fuse-anchor\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n",
			filepath.Join(root, "fuse-anchor", "extension.go"): "package fuseanchor\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\nfunc Extension() *sdk.Extension { return sdk.New(\"fuse-anchor\") }\n",
		})
	}
	writeFiles(t, map[string]string{
		filepath.Join(root, "lispbin", "lispbin.asd"): "(asdf:defsystem \"lispbin\"\n  :depends-on (\"pig-sdk\")\n  :serial t\n  :components ((:file \"lispbin\")))\n",
		filepath.Join(root, "lispbin", "lispbin.lisp"): `(defpackage "lispbin" (:use "COMMON-LISP"))
(in-package "lispbin")

(pig:define-extension "lispbin"
  (:tool "echo_bridge" "Echo text back"
         (pig:obj "type" "object" "properties" (pig:obj "text" (pig:obj "type" "string")) "required" #("text"))
         (lambda (ctx params) (declare (ignore ctx)) (format nil "LISP-BINARY-ECHO ~A" (pig:jget params "text"))))
  (:command "lisp-mark" "write the file named by the argument"
            (lambda (ctx args) (declare (ignore ctx))
              (with-open-file (out args :direction :output :if-exists :supersede :external-format :utf-8)
                (write-string "LISP-MARK" out)))))
`,
		filepath.Join(root, "lispbin.yaml"): "name: lispbin\nextensions:\n" + members,
	})
	return filepath.Join(root, "lispbin.yaml")
}

// A Piglet Binary carries a Lisp extension: the plan lists it as a binary-packed component, so the build embeds its SBCL image and the
// Binary starts it from the embedded cell, with no SBCL on PATH, as the planned closure requires. Before the build embedded the cell, it
// warned [not-embedded]; a Piglet with nothing else to fuse failed with "nothing to embed or fuse", and one with a fused member built a
// Binary that exited at startup with "baked Piglet Binary binary-packed extensions do not match its plan: missing [lispbin]".
func TestPigletBinaryRunsItsLispExtension(t *testing.T) {
	if _, err := exec.LookPath(toolchain.SBCLExecutable(runtime.GOOS, exec.LookPath)); err != nil {
		t.Skipf("sbcl is required to build a Lisp cell: %v", err)
	}
	for _, tc := range []struct {
		name  string
		fused bool
	}{{"lisp member alone", false}, {"with a fused Go member", true}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			isolateBuildHome(t, root)
			binary := filepath.Join(root, nativeBinaryName("pig-lispbin"))
			piglet := writeLispPiglet(t, root, tc.fused)
			var stdout, stderr strings.Builder
			if code := runBuild([]string{piglet, "--format", "binary", "--builder", "native", "--out", binary}, &stdout, &stderr); code != 0 {
				t.Fatalf("pig piglet build exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "Checksumming embedded cells") || strings.Contains(stderr.String(), "not-embedded") {
				t.Fatalf("the build did not embed the Lisp cell:\n%s", stderr.String())
			}
			// The Binary runs the cell it embeds: nothing on PATH builds or finds an SBCL.
			noToolchain := t.TempDir()

			t.Run("starts", func(t *testing.T) {
				if code, output := startPigletBinary(t, binary); code != 0 {
					t.Fatalf("Piglet Binary did not start: exit %d\n%s", code, output)
				}
			})
			t.Run("tool", func(t *testing.T) {
				code, out, errOut, _ := runNodeBinary(t, binary, noToolchain, "--mode", "json", "-p", "Run: extension echo hello")
				if code != 0 || !strings.Contains(out, "LISP-BINARY-ECHO hello") {
					t.Fatalf("exit %d; the extension tool did not run\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
				}
			})
			t.Run("command", func(t *testing.T) {
				mark := filepath.Join(t.TempDir(), "lisp-mark.txt")
				code, out, errOut, _ := runNodeBinary(t, binary, noToolchain, "-p", "/lisp-mark "+mark)
				got, readErr := os.ReadFile(mark)
				if code != 0 || readErr != nil || string(got) != "LISP-MARK" {
					t.Fatalf("exit %d, mark %q (%v); the extension command did not run\nstdout:\n%s\nstderr:\n%s", code, got, readErr, out, errOut)
				}
			})
		})
	}
}
