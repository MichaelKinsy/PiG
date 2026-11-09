// Package cachewarming is p2's cache warming plugin (apps/p2/plugins/cache-warming): the prompt cache of each conversation's
// last request is kept warm as pi-coding-agent keeps its session's, by pi-ai's policy.
package cachewarming

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	worker := NewFacet()
	hosts.RegisterPlugin(hosts.Plugin{Name: "cache-warming", Worker: &worker})
}
