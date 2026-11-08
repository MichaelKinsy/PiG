package models

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	hosts.RegisterPlugin(hosts.Plugin{Name: "models", Server: &serverFacet, Worker: &workerFacet})
}
