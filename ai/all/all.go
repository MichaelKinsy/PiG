// Package all links every built-in provider package under ai/providers, so a program that imports it offers the same providers as pig does. A plain import of ai links only the providers that ai itself implements; a program that wants the rest imports this package or a single provider package, for example
//
//	import _ "github.com/MichaelKinsy/PiG/ai/providers/bedrock"
package all

import (
	_ "github.com/MichaelKinsy/PiG/ai/providers/bedrock" // amazon-bedrock, bedrock-converse-stream
)
