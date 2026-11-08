package sharing

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	server := Server
	hosts.RegisterPlugin(hosts.Plugin{Name: "sharing", Server: &server})
}
