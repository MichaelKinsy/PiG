//go:build !pig_strip_llama_cpp

package cli

import (
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// llamaInlineExtensions is the built-in llama.cpp extension's entry of nativeBuiltInExtensions.
//
// This file and the internal/codingagent/llama import are the boundary a Piglet Binary strips with the pig_strip_llama_cpp
// build tag (llama_stripped.go).
// pig additive (D92): a stripped llama.cpp has no entry, as in a Binary that compiled it out.
func llamaInlineExtensions() []extension.InlineExtension {
	if pigstrip.Has(pigstrip.ListExtensions, llamaBuiltinName) {
		return nil
	}
	return []extension.InlineExtension{extension.NamedInlineExtension{Name: llamaBuiltinName, Factory: llamaExtension, Builtin: true}}
}

// llamaExtension is the built-in llama.cpp extension: it registers the llama.cpp provider object with the model runtime and the
// /llama command that manages its router models, like Pi's factory.
// Ports .upstream/v1.1.0/packages/coding-agent/src/extensions/llama/index.ts (registerProvider, registerCommand "llama").
func llamaExtension(pi extension.API) error {
	controller := llama.CreateLlamaProvider()
	pi.RegisterNativeProvider(controller.ModelsProvider())
	pi.RegisterCommand(llama.CommandName, extension.CommandOptions{Description: llama.CommandDescription, Handler: llama.CommandHandler(controller)})
	return nil
}
