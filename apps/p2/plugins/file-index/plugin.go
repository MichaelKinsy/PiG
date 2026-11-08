package fileindex

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	worker := WorkerFacet
	hosts.RegisterPlugin(hosts.Plugin{Name: "file-index", Worker: &worker})
}
