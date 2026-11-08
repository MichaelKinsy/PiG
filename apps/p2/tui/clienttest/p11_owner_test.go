package clienttest

import (
	"context"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	devicescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/devices/contract"
	devicestui "github.com/MichaelKinsy/PiG/apps/p2/plugins/devices/tui"
	envscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	envstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/tui"
	logscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/logs/contract"
	logstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/logs/tui"
	portscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/ports/contract"
	portstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/ports/tui"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	sharingtui "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/tui"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	statustui "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/tui"
	chattui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/tui"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// ownerFacets is test/p2/tui-p11.test.ts OWNER_FACETS.
func ownerFacets() []host.Facet {
	return []host.Facet{tui.CoreFacet, chattui.NewFacet(), statustui.Facet, logstui.Facet, envstui.Facet, portstui.Facet, devicestui.Facet, sharingtui.Facet}
}

// ownerCells are the state the tests change later (`cells`).
type ownerCells struct {
	mu      sync.Mutex
	notices *p2test.Cell
}

func (o *ownerCells) Notices() *p2test.Cell {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.notices
}

func ok(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }

// ownerStubs are the owner-only server objects (P11d-g): each records calls.
func ownerStubs(c *calls) ([]p2test.Stub, *ownerCells) {
	cells := &ownerCells{}
	hostKey := delta.JsonObjectOf("state", "unknown", "fingerprints", []any{"SHA256:abc"}, "lines", []any{"box ssh-ed25519 AAAA"})
	recorded := func(name string, key string) p2test.StubMethod {
		return func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
			if key == "" {
				c.record(name, args)
			} else {
				c.record(name, args.(*delta.JsonObject).Value(key))
			}
			return nil, nil
		}
	}
	notice := func(id float64, source, message string) *delta.JsonObject {
		return delta.JsonObjectOf("id", id, "time", id, "source", source, "message", message)
	}
	tokens := 0
	stubs := []p2test.Stub{
		{Contract: p2runtime.LogNotices, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
			c.record("noticesAcquired", true)
			notices := self.Cell([]any{notice(1, "server", "old trouble")})
			cells.mu.Lock()
			cells.notices = notices
			cells.mu.Unlock()
			return p2test.StubMembers{Variant: "owner", State: map[string]any{"notices": notices}, Methods: map[string]p2test.StubMethod{
				"dismiss": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					id := args.(*delta.JsonObject).Value("id")
					c.record("dismiss", id)
					return nil, notices.Change(func(current any) any {
						var kept []any
						for _, each := range current.([]any) {
							if each.(*delta.JsonObject).Value("id") != id {
								kept = append(kept, each)
							}
						}
						return kept
					})
				},
			}}
		}},
		{Contract: logscontract.Logs, Factory: func(params *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
			source := params.Value("source").(string)
			c.record("logs", source)
			return p2test.StubMembers{Variant: "owner", State: map[string]any{
				"sources": []any{delta.JsonObjectOf("id", "session:s", "lines", float64(1), "updatedAt", float64(1))},
				"lines": []any{
					delta.JsonObjectOf("id", float64(1), "time", float64(1), "level", "stdout", "text", "first line of "+source),
					delta.JsonObjectOf("id", float64(2), "time", float64(2), "level", "stderr", "text", "last line of "+source),
				},
			}}
		}},
		{Contract: statuscontract.SessionStatus, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{Variant: "reader", State: map[string]any{"statuses": delta.JsonObjectOf(
				"s", delta.JsonObjectOf("running", false, "error", "worker exited with code 1", "tasks", float64(0), "busy", []any{}),
			)}}
		}},
		{Contract: statuscontract.WorkerControl, Factory: func(params *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{Variant: "owner", Methods: map[string]p2test.StubMethod{
				"restart": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					c.record("retry", params.Value("session"))
					return nil, nil
				},
			}}
		}},
		{Contract: envscontract.Envs, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
			listing := delta.JsonObjectOf("path", "/", "parent", nil, "directories", []any{}, "truncated", false)
			stopped := delta.JsonObjectOf("state", "stopped")
			return p2test.StubMembers{Variant: "owner", State: map[string]any{
				"envs": []any{
					delta.JsonObjectOf("name", "box", "config", delta.JsonObjectOf("type", "ssh", "host", "box.example", "user", "me"), "sessions", float64(2)),
					delta.JsonObjectOf("name", "lab", "config", delta.JsonObjectOf("type", "ssh", "host", "lab.example"), "sessions", float64(0),
						"test", delta.JsonObjectOf("ok", false, "testedAt", float64(1), "error", "host key not trusted", "hostKey", hostKey)),
				},
				"failure": nil,
			}, Methods: map[string]p2test.StubMethod{
				"test": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					c.record("test", args.(*delta.JsonObject).Value("name"))
					return delta.JsonObjectOf("ok", true, "testedAt", float64(2), "platform", "linux", "arch", "x64", "home", "/home/me", "version", "1", "latencyMs", float64(12), "watchMode", "native", "warnings", []any{}), nil
				},
				"acceptHostKey": recorded("acceptHostKey", ""),
				"forgetHostKey": recorded("forgetHostKey", ""),
				"list":          func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return []any{}, nil },
				"warm": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					c.record("warm", true)
					return nil, nil
				},
				"save": ok, "delete": ok,
				"generateKey":      func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return delta.JsonObjectOf("publicKey", "", "identityFile", ""), nil },
				"uploadKey":        func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return delta.JsonObjectOf("publicKey", "", "identityFile", ""), nil },
				"publicKey":        ok,
				"listDirectory":    func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return listing, nil },
				"sandboxStatus":    func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return stopped, nil },
				"sandboxStart":     func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return stopped, nil },
				"sandboxStop":      func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return stopped, nil },
				"sandboxResetDisk": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return stopped, nil },
			}}
		}},
		{Contract: portscontract.Ports, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{Variant: "owner", State: map[string]any{
				"forwards": []any{
					delta.JsonObjectOf("id", "f1", "port", float64(5173), "name", "vite", "url", "https://fwd--"+strings.Repeat("a", 32)+"--x1.radius.example/"),
					delta.JsonObjectOf("id", "f2", "port", float64(9000), "url", "https://evil.example/phish"),
				},
				"radiusOrigin": "https://radius.example",
			}, Methods: map[string]p2test.StubMethod{
				"add": recorded("addPort", ""), "remove": recorded("removePort", ""), "setPublic": recorded("setPublic", ""),
			}}
		}},
		{Contract: sharingcontract.GuestPresence, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{Variant: "owner"}
		}},
		{Contract: sharingcontract.Sharing, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
			c.record("sharingAcquired", true)
			links := self.Cell([]any{})
			return p2test.StubMembers{Variant: "owner", State: map[string]any{
				"env": "box", "links": links, "guests": []any{delta.JsonObjectOf("id", "g1", "name", "Alice", "mode", "view")},
				"people": []any{}, "peopleUrl": nil,
			}, Methods: map[string]p2test.StubMethod{
				"share":  recorded("share", ""),
				"remove": recorded("removeGuest", ""),
				"link": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					c.record("link", object)
					tokens++
					mode := object.Value("mode")
					token := "tok-" + mode.(string) + "-" + string(rune('0'+tokens))
					return nil, links.Change(func(current any) any {
						var next []any
						for _, each := range current.([]any) {
							if each.(*delta.JsonObject).Value("mode") != mode {
								next = append(next, each)
							}
						}
						return append(next, delta.JsonObjectOf("mode", mode, "token", token, "url", "https://share.test/join/"+token))
					})
				},
			}}
		}},
		{Contract: devicescontract.Devices, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{Variant: "owner", State: map[string]any{"canMint": true}, Methods: map[string]p2test.StubMethod{
				"token": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					c.record("token", true)
					return delta.JsonObjectOf("token", "one-time-token-123"), nil
				},
			}}
		}},
	}
	return stubs, cells
}

// grants are the launcher answers a test records: onGrant answers them.
type grants struct {
	mu   sync.Mutex
	list [][2]any
}

func (g *grants) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.list)
}

func (g *grants) at(i int) [2]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.list[i]
}

// answer installs the launcher bridge: every grant is recorded and answered with result.
func (g *grants) answer(s *p11Setup, result tuiruntime.GrantResult) {
	s.Do(func() {
		s.Runtime.OnGrant = func(action string, payload any) tuiruntime.GrantResult {
			g.mu.Lock()
			g.list = append(g.list, [2]any{action, payload})
			g.mu.Unlock()
			return result
		}
	})
}
