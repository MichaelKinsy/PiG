package envs

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	server := serverFacet
	hosts.RegisterPlugin(hosts.Plugin{Name: "envs", Server: &server})
}
