package history

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	worker := Worker
	hosts.RegisterPlugin(hosts.Plugin{Name: "history", Worker: &worker})
}
