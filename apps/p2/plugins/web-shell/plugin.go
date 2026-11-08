// Package webshell is p2's `web-shell` plugin: the app layout (sidebar and main slots, the drawer on phones, Back/Forward
// navigation). It has only a web facet (web/upstream/plugins/web-shell/web.tsx, part of the embedded bundle), so there is
// nothing to load on the server or in a worker; it is registered so that plugin selection and the bundle agree on it.
package webshell

import "github.com/MichaelKinsy/PiG/apps/p2/hosts"

func init() {
	hosts.RegisterPlugin(hosts.Plugin{Name: "web-shell"})
}
