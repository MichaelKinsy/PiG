//go:build pig_strip_node_extensions

package subprocess

import (
	"context"
	"io/fs"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// pig additive (D92): this Piglet Binary compiled out the embedded Node runtime, so TypeScript and JavaScript extensions report the strip error instead of loading.
func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.NodeExtensions) }

var nodeRuntimeFS nodeArchiveFS

type nodeArchiveFS struct{}

func (nodeArchiveFS) Open(string) (fs.File, error) { return nil, errNodeExtensionsStripped() }

func nodeRuntimeUnavailable() error { return errNodeExtensionsStripped() }

func ensureNodeRuntime(context.Context) (string, error) { return "", errNodeExtensionsStripped() }
