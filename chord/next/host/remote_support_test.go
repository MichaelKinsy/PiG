package host

import "context"

// rhLoadErr is `await host.load(facets, ctx)` of a test that only waits for it: the error of Load, its report dropped.
func rhLoadErr(host Host, facets []HostFacet, ctx context.Context) error {
	_, err := host.Load(facets, ctx)
	return err
}
