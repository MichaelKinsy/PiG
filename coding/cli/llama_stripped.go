//go:build pig_strip_llama_cpp

package cli

import "github.com/MichaelKinsy/PiG/coding/extension"

// pig additive (D92): this build has no built-in llama.cpp extension (internal/codingagent/llama is not linked). It is not in
// the built-in extension list, no llama host starts, and `/llama` is not a command; internal/codingagent's OFF shim records the
// strip, so `builtin:llama.cpp` in settings or `-e` is skipped like any stripped built-in.

func llamaInlineExtensions() []extension.InlineExtension { return nil }
