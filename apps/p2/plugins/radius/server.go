// Package radius is p2's Radius plugin (apps/p2/plugins/radius): p2 as a Radius brain, reachable from anywhere Radius is,
// and its `p2 radius-login` command.
package radius

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	portscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/ports/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/radius/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/plugins/radius/server.ts and login.ts.

func objectOf(value any) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

// StatusText is Pi Mobile's status lines: the brain it connected as, or why it is not connected (retrying).
func StatusText(status contract.Status) string {
	switch status.Kind {
	case contract.StatusConnected:
		return "connected as brain " + status.ServerID
	case contract.StatusRetrying:
		return "not connected (" + status.Error + ")"
	}
	return ""
}

// forwardsOf adapts the ports plugin's store to what the brain relays to.
type forwardsOf func() *portscontract.Forwards

func (f forwardsOf) Lookup(id string) (contract.ForwardTarget, bool) {
	forward, ok := f().Get(id)
	return contract.ForwardTarget{Port: forward.Port, Public: forward.Public}, ok
}

func (f forwardsOf) RadiusOrigin() string {
	if origin := f().RadiusOrigin(); origin != nil {
		return *origin
	}
	return ""
}

func (f forwardsOf) SetRadiusOrigin(origin string) { f().SetRadiusOrigin(&origin) }

var serverFacet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := contract.ServeRadiusConnections(facet); err != nil {
		return err
	}
	return facet.Serve(contract.RadiusService, host.LocalFactory(serveRadiusService), host.ServeOptions{})
})

func serveRadiusService(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
	self.Get(portscontract.ForwardStore, delta.NewJsonObject(0))
	forwards := portscontract.ForwardsOf(self)
	opened := self.Open(
		func(context.Context) (any, error) {
			value, err := server.Member("info").Value()
			if err != nil {
				return nil, err
			}
			info := objectOf(value)
			dataDir, _ := info.Value("dataDir").(string)
			port, _ := info.Value("port").(float64)
			parts, err := forwards()
			if err != nil {
				return nil, err
			}
			relay := contract.NewRelayAccess()
			lastStatus := ""
			brain := contract.NewBrain(contract.BrainOptions{
				Directory: dataDir,
				Port:      func() int { return int(port) },
				Relay:     relay,
				Forwards:  forwardsOf(parts.Forwards),
				// As Pi Mobile: each new status once, retries with their error.
				OnStatus: func(status contract.Status) {
					if text := StatusText(status); text != "" {
						if text != lastStatus {
							fmt.Printf("Radius: %s\n", text)
						}
						lastStatus = text
					}
				},
			})
			if os.Getenv("P2_RADIUS") != "0" {
				brain.Start()
			}
			return contract.Connection{Relay: relay, Brain: brain, Port: int(port)}, nil
		},
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(contract.Connection).Brain.Close()
			return nil
		},
	)
	connection := func() contract.Connection {
		value, err := opened.Value()
		if err != nil {
			panic(err)
		}
		return value.(contract.Connection)
	}
	store := self.(host.StoreSelf)
	p2runtime.ContributeKeyed(store, p2runtime.RequestResolvers, p2runtime.Entry("radius", p2runtime.RequestResolver(
		func(request *http.Request, _ host.Ctx) (*p2runtime.RequestAccess, error) {
			current := connection()
			relayed, err := current.Relay.Verify(request)
			if err != nil || relayed == nil {
				return nil, err
			}
			origin := p2runtime.RequestOrigin{Origin: relayed.PublicOrigin, Remote: true, Port: current.Port, RelayShellOrigin: relayed.ShellOrigin}
			// Anyone else is whom sharing finds by their email (the sessions shared with them), or nobody.
			if relayed.User == nil {
				return &p2runtime.RequestAccess{Principal: p2runtime.OwnerPrincipal{Access: p2runtime.AccessRelay}, Origin: origin}, nil
			}
			origin.RelayUser = relayed.User
			return &p2runtime.RequestAccess{Origin: origin}, nil
		})))
	// Radius admits and lists this brain for the emails sessions are shared with.
	shares := self.Get(sharingcontract.Shares, delta.NewJsonObject(0))
	self.On(shares.Member("emails"), func(call chord.WatchCall[delta.JsonValue], _ host.Ctx) *host.Handled {
		emails := []string{}
		list, _ := call.Value.([]any)
		for _, each := range list {
			text, _ := each.(string)
			emails = append(emails, text)
		}
		connection().Brain.ReportShares(emails)
		return nil
	})
	p2runtime.ContributeKeyed(store, p2runtime.ShareUrls, p2runtime.Entry("radius", p2runtime.ShareURL(func(session string) (string, bool) {
		page := connection().Brain.ShellPage()
		if page == "" {
			return "", false
		}
		return page + "?" + url.Values{"path": {"/s/" + session}}.Encode(), true
	})))
	store.Contribute(contract.RadiusConnections, &contract.RadiusConnectionsParts{Connection: connection})
	return contracts.Implementation{}, nil
}

func init() {
	hosts.RegisterSubcommand("radius-login", radiusLogin)
}

// radiusLogin is `p2 radius-login` (login.ts): Radius is separate from model-provider login. Device-code sign-in works on a
// phone or over SSH.
func radiusLogin(_ []string) int {
	directory := os.Getenv("P2_DATA_DIR")
	if directory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		directory = home + "/.pi/p2"
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	existing, err := contract.ReadConfig(directory)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	gateway := contract.GatewayFromEnv()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Printf("Signing in to Radius at %s\n", gateway)
	credential, err := contract.Login(ctx, gateway, func(url, code string) {
		fmt.Printf("Open %s and enter the code %s\n", url, code)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	config := contract.Config{Gateway: gateway, ServerID: contract.NewServerID(), Credential: credential}
	if existing != nil && existing.Gateway == gateway {
		config.ServerID = existing.ServerID
		config.PublicOrigin = existing.PublicOrigin
	}
	if err := contract.WriteConfig(directory, config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("Signed in. A running p2 connects to Radius within 30 seconds.")
	return 0
}
