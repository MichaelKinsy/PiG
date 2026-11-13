### Changed

- `extensions/sdk-ts`, `extensions/sdk-lisp/src`, `internal/pigdocs/content` and `chord/nextnode/http/testdata` are directories with their own `go.mod`, so the root module zip that `go install` downloads is 130.7 MB instead of 135.3 MB (4.6 MB of TypeScript declarations, Common Lisp sources, docs pages and recorded URL corpora that no Go package embeds or imports). The files stay in the repository; PiG still carries the Lisp SDK and the docs in their embedded packs, and npm, ASDF, tests, generators and drift gates read the directories by path.
