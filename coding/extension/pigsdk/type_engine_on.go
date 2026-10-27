//go:build !(pig_strip_extension_sdk_go && pig_strip_extension_sdk_rust && pig_strip_extension_sdk_python)

package pigsdk

import (
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/typebox/engine"
)

// pig additive (D92): the Go, Rust and Python SDKs run TypeBox's type engine in the host (the type.engine call); the Node SDK runs TypeBox itself. A build that embeds
// any of the three SDKs registers the host's engine, and a program that links the extension host without this package, or with all three SDKs stripped, registers none.
func init() { subprocess.SetTypeEngine(engine.Serve) }
