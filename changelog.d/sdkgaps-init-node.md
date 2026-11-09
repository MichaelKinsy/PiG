### Added

- `pig extension init <path> --lang node` scaffolds a TypeScript extension (`package.json` and an `index.ts` default-export factory with a ping tool, a command and a status) like the Go, Python, Rust and Lisp scaffolds. `--isolated` is refused for Node, which has no SDK for an executable script to speak the wire with.
