package files

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	server, worker := Server, Facet()
	hosts.RegisterPlugin(hosts.Plugin{Name: "files", Server: &server, Worker: &worker})
}
