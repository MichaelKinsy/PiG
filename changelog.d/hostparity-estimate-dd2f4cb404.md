### Fixed

- The Go SDK's `EstimateTokens` counts an unpaired surrogate as one UTF-16 unit, as Pi's `estimateTokens` does, and as the six-character escape inside a tool call's arguments. A message with eight unpaired surrogates sized 6 tokens where Pi gives 2, and one inside a tool call argument sized less than Pi's `JSON.stringify` length. The Python and Lisp SDKs were right and now have the same table of Pi's numbers; the Rust SDK cannot receive such a string. <!-- fix: dd2f4cb404 -->
