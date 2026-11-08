package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The contracts' fingerprints equal Pi's (see sessions/contract).
func TestDevicesContractsFingerprintAsPi(t *testing.T) {
	const module = "plugins/devices/contract.ts"
	pi := oracle.Fingerprints(t, module)
	for export, ours := range map[string][3]any{
		"DeviceAccess": {DeviceAccess.Name, DeviceAccess.Version, DeviceAccess.Fingerprint},
		"Devices":      {Devices.Name, Devices.Version, Devices.Fingerprint},
	} {
		want, ok := pi[module+"#"+export]
		if !ok {
			t.Fatalf("Pi exports no %s", export)
		}
		if ours[0] != want.Name || ours[1] != want.Version || ours[2] != want.Fingerprint {
			t.Errorf("%s = %v, Pi has {%s v%d %s}", export, ours, want.Name, want.Version, want.Fingerprint)
		}
	}
}
