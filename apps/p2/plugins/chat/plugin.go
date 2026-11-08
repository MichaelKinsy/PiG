package chat

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	worker := Facet()
	hosts.RegisterPlugin(hosts.Plugin{Name: "chat", Worker: &worker})
}
