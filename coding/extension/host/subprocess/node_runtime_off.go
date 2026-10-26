//go:build pig_strip_node_extensions

package subprocess

import (
	"context"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// pig additive (D92): this Piglet Binary compiled out the embedded Node runtime, so TypeScript and JavaScript extensions report the strip error instead of loading.
func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.NodeExtensions) }

func extractNodeRuntime(string) error { return errNodeExtensionsStripped() }

func nodeRuntimeUnavailable() error { return errNodeExtensionsStripped() }

func ensureNodeRuntime(context.Context) (string, error) { return "", errNodeExtensionsStripped() }
