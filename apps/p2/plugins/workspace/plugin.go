package workspace

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	server, worker := ServerFacet, WorkerFacet
	hosts.RegisterPlugin(hosts.Plugin{Name: "workspace", Server: &server, Worker: &worker})
}
