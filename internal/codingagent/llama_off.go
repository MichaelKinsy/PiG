//go:build pig_strip_llama_cpp

package codingagent

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// pig additive (D92): this build has no built-in llama.cpp extension. Nothing registers the llama.cpp provider or /llama, and
// `builtin:llama.cpp` is skipped like a stripped built-in.
func init() {
	pigstrip.Strip(pigstrip.ListExtensions, strings.TrimPrefix(LlamaExtensionPath, BuiltinPathPrefix))
}
