package plugins

import (
	"context"
	"errors"
)

// pluginsSpecifier is the one specifier of this component a script imports (`pluginFile`): external always.
const pluginsSpecifier = "@earendil-works/chord/next/plugins"

func esbuildFacet(plugin Plugin, kind string, options BuildOptions, ctx context.Context) (BuiltFacet, error) {
	return BuiltFacet{}, errors.New("esbuildFacet: not implemented")
}
