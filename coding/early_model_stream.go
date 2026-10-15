package coding

import (
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// An extension host with no UI bridge (an SDK session, the extension validate and login loaders) runs the pi-ai calls a module makes while it loads through the
// global API providers, as Pi does in the extension's own process.
func init() {
	subprocess.SetEarlyModelStream(icodingagent.EarlyModelStream(""))
}
