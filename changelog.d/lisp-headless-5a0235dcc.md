### Upgrading

- Go API `coding/extension/host/subprocess` and `coding/extension/source`: a program that loads Lisp extensions imports the new package `coding/extension/host/lispcell` for its side effect (`import _ "github.com/MichaelKinsy/PiG/coding/extension/host/lispcell"`). The `pig` command does. A program that links the extension host without it, such as `examples/headless`, does not resolve Lisp extension sources and fails a Lisp cell with "this program was built without Lisp extension support". `subprocess.EnableLisp` and `source.EnableLisp` are the hooks the package calls.

### Changed

- `examples/headless` no longer links the Lisp image build, the Quicklisp resolver or the Lisp source resolver (about 78 KB on linux/amd64), so its default binary is back under its size ceilings on all four release platforms. The Go, Rust and Python cell staging of the extension host shares one code path with the same error messages.
