package mcp

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	server, worker := Server, Worker
	hosts.RegisterPlugin(hosts.Plugin{Name: "mcp", Server: &server, Worker: &worker})
}
