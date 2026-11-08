// Command seed fills a running p2 server with the sessions the visual gate photographs, through the Chord Sessions contract
// over /chord (as the web UI and the app do): several locations, one pinned, one that has a faux-provider exchange.
//
//	seed <port> <scratch dir>
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: seed <port> <scratch dir>")
		os.Exit(2)
	}
	var port int
	fmt.Sscan(os.Args[1], &port)
	conn, err := p2test.Dial(port, nil)
	check(err)
	browser, err := p2test.ClientHost(conn)
	check(err)
	defer func() { _ = browser.Close(p2test.Background) }()
	sessions, err := p2test.HoldIn(browser, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	check(err)
	defer sessions.Release()
	var pin string
	for _, s := range []struct{ dir, title string }{
		{"pig", "Fix the flaky sync test"}, {"pig", "Port the markdown renderer"}, {"pig", "Explain the surface host"},
		{"site", "Write the release notes"}, {"site", "Tighten the landing page"}, {"notes", "Plan the week"},
	} {
		dir := filepath.Join(os.Args[2], s.dir)
		check(os.MkdirAll(dir, 0o755))
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", dir, "title", s.title), p2test.Background)
		check(err)
		if pin == "" {
			if o, ok := created.(*delta.JsonObject); ok {
				pin, _ = o.Value("id").(string)
			}
		}
	}
	if pin != "" {
		_, err := sessions.Call("pin", delta.JsonObjectOf("id", pin, "pinned", true), p2test.Background)
		check(err)
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}
