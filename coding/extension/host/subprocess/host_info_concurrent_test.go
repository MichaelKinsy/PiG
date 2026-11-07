package subprocess_test

import (
	"net"
	"os"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// TestFusedExtensionsOfConcurrentHostsSeeTheHostInfoEntry: a fused extension's factory runs in the host process, so its Host sets PIG_EXT_HOST_INFO in the process environment (D31). A --listen server loads one Host per session, concurrently, and every Host of one server records the same HostInfo, so each factory must read the one entry and the concurrent writes must not race (D232; run with -race).
func TestFusedExtensionsOfConcurrentHostsSeeTheHostInfoEntry(t *testing.T) {
	const hosts = 16
	info := subprocess.HostInfo{AgentDir: "/agent", ConfigDirName: ".pig", Version: "0.0.0-test", PackageDir: "/package"}
	var wg sync.WaitGroup
	seen := make([]string, hosts)
	errs := make([]error, hosts)
	for i := range hosts {
		wg.Go(func() {
			h := subprocess.NewHost(t.TempDir())
			h.SetUIBridge(subprocess.NewUIBridge(func() {}))
			h.SetHostInfo(info)
			defer h.Shutdown("test done")
			ext := sdk.New("host-info-probe")
			serve := func(conn net.Conn) error {
				seen[i] = os.Getenv("PIG_EXT_HOST_INFO")
				return ext.RunWithConn(conn)
			}
			_, errs[i] = h.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "host-info-probe", Enabled: true}, serve)
		})
	}
	wg.Wait()
	for i := range hosts {
		if errs[i] != nil {
			t.Fatalf("host %d: %v", i, errs[i])
		}
		if seen[i] == "" || seen[i] != seen[0] {
			t.Fatalf("host %d factory saw PIG_EXT_HOST_INFO %q, host 0 saw %q", i, seen[i], seen[0])
		}
	}
}
