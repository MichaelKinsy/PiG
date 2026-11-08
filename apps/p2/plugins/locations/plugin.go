package locations

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	server, worker := serverFacet, workerFacet
	hosts.RegisterPlugin(hosts.Plugin{Name: "locations", Server: &server, Worker: &worker})
}
