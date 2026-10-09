//go:build pig_strip_docs

package prompts

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// pig additive (D92): this Piglet Binary compiled out the PiG documentation bundle, so the prompt has no docs section.
func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.Docs) }

// docsSections is empty: this build writes no docs for the model to read.
func docsSections(string, string, string) []section { return nil }
