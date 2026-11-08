### Changed

- The Go, Python, Rust and Lisp SDKs no longer have a `readFile` member on the grep tool's operations; the host never called it since grep takes its context lines from ripgrep.
