### Fixed

- CI: the Linux parity shards install the pinned SBCL as the test and portmap shards do, so the Lisp scenario `75-robust-notes-lisp` no longer fails with `exec "sbcl" not found in PATH` on whichever parity shard runs it. <!-- fix: f6d681159b -->
