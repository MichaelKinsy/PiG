package voice

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	server, worker := serverFacet, workerFacet
	hosts.RegisterPlugin(hosts.Plugin{Name: "voice", Server: &server, Worker: &worker})
}
