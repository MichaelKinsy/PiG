// Package lispcell turns on Lisp extension support in a program: importing it for its side effect lets the extension host build, find and
// start Lisp runner images and lets the source resolver classify Lisp extension roots. cmd/pig imports it. A program that links the extension host
// without it, such as an embedding program, carries none of the SBCL image build, the Quicklisp resolver or the Lisp source resolver, and a Lisp
// extension fails there with "this program was built without Lisp extension support".
package lispcell

import (
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/extension/source"
)

func init() {
	subprocess.EnableLisp()
	source.EnableLisp()
}
