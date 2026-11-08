package chatmode

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	worker := Worker
	hosts.RegisterPlugin(hosts.Plugin{Name: "chat-mode", Worker: &worker})
}
