### Fixed

- `pig piglet build` planned a Common Lisp extension as packed into the Piglet Binary but did not embed its SBCL image: it warned `[not-embedded]`, a Piglet with no other member to fuse failed with "nothing to embed or fuse", and a Piglet with a fused member built a Binary that exited at startup with `baked Piglet Binary binary-packed extensions do not match its plan: missing [<extension>]`. The build now builds the Lisp image and embeds it, the Binary starts it without SBCL, and a Piglet whose only extensions are Lisp extensions builds. <!-- fix: 20b9d0592b -->
