package prompt

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	worker := WorkerFacet
	hosts.RegisterPlugin(hosts.Plugin{Name: "prompt", Worker: &worker})
}
