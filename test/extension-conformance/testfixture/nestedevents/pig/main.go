// Command nested-events-pig is pig with the nested-event conformance row's Go extension fused in, as a Piglet Binary fuses
// one (fusepack.Register): the host serves the extension named nested-go-fused in this process instead of building it.
package main

import (
	"net"
	"os"

	"github.com/MichaelKinsy/PiG/coding/cli"
	"github.com/MichaelKinsy/PiG/coding/extension/host/fusepack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/nestedevents"
)

// fusedName is the extension the resolver serves in process: the test loads a factory directory of this name with -e.
const fusedName = "nested-go-fused"

type resolver struct{}

func (resolver) FusedServe(cfg subprocess.ExtConfig) (func(net.Conn) error, bool) {
	if cfg.Name != fusedName {
		return nil, false
	}
	return func(conn net.Conn) error {
		defer func() { _ = conn.Close() }()
		return nestedevents.Extension(fusedName).RunWithConn(conn)
	}, true
}

func main() {
	subprocess.SetFusedResolver(resolver{})
	sdk.SetHostHelpers(fusepack.HostHelpers())
	cli.Main(os.Args[1:], nil)
}
