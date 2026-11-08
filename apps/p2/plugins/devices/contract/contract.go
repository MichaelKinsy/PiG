// Package contract ports apps/p2/plugins/devices/contract.ts (and owner-access.ts, the access it carries): the
// installation-owned key and one-time tokens, and the owner's Devices (a leaf package).
package contract

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/schema"
)

func must(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

// DeviceAccess is the installation-owned key and one-time tokens (`p2.device-access`, open while held; its access is the
// `DeviceAccesses` native). Neither the key nor the cookie ever enters web state.
var DeviceAccess = must(contracts.Local("p2.device-access", contracts.LocalDeclaration{Version: 2, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))

// DeviceAccessParts is `DeviceAccesses`' value: the open `DeviceAccess`'s owner access.
type DeviceAccessParts struct{ Access func() *OwnerAccess }

// DeviceAccesses is the registry of the open `DeviceAccess`'s access (`p2.device-accesses`): its value is the
// contribution, nil while it is not open.
var DeviceAccesses = p2runtime.NewNativeRegistry("p2.device-accesses")

// ServeDeviceAccesses serves the registry (the devices plugin's facet; test hosts).
func ServeDeviceAccesses(facet host.FacetHost) error {
	return p2runtime.ServeNative[DeviceAccessParts](facet, DeviceAccesses)
}

// DeviceAccessesOf is the open access for a factory, read at each use: it declares the registry.
func DeviceAccessesOf(self host.Self) func() (*DeviceAccessParts, error) {
	return p2runtime.NativeOf[DeviceAccessParts](self.Registry(DeviceAccesses), "The owner access")
}

// Devices: all owners may open it; only the localhost device owner may mint an association token.
var Devices = must(contracts.Remote("p2.devices", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{p2runtime.StateProp("canMint", schema.Bool())},
			Methods: []contracts.MethodEntry{
				contracts.Meth("token", contracts.NewMethod(contracts.Schema{}, p2runtime.Contract(schema.Obj(schema.Properties{schema.Prop("token", schema.Str())})))),
			},
		}),
	},
}))
