package subagents

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	server := serverFacet
	worker := workerFacet()
	hosts.RegisterPlugin(hosts.Plugin{Name: "subagents", Server: &server, Worker: &worker})
}
