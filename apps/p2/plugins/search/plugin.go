package search

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	server, worker := ServerFacet, WorkerFacet
	hosts.RegisterPlugin(hosts.Plugin{Name: "search", Server: &server, Worker: &worker})
}
