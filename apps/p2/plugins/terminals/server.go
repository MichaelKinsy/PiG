package terminals

import (
	"context"
	"fmt"
	"net/http"
	"os"

	sessioncontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	subagents "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/plugins/terminals/server.ts.

// The size of a terminal the agent opens; the owner's panel resizes it to fit once it shows it.
const (
	agentCols = 120
	agentRows = 30
)

// heldView is an open view with the service that made it.
type heldView struct {
	view    *TerminalView
	service *TerminalService
}

// ServiceParts is what the open `TerminalServiceObject` contributes to `TerminalServices`: the service, read at each use.
type ServiceParts struct {
	Service func() (*TerminalService, error)
}

func jsonObject(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func stringOf(value any) string {
	text, _ := value.(string)
	return text
}

func intOf(value any) int {
	number, _ := value.(float64)
	//portlint:allow numbers the schema bounds every size and count this reads
	return int(number)
}

// ServerFacet is the plugin's server facet.
var ServerFacet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := p2runtime.ServeNative[ServiceParts](facet, contract.TerminalServices); err != nil {
		return err
	}
	// Archiving a session ends its programs, whether or not anyone holds its terminals now (the service connects to the
	// host only when it runs); archiving goes on when the host cannot be reached.
	if err := p2runtime.Contribute(facet, "p2.terminals-archived", func(self host.StoreSelf) error {
		self.Get(contract.TerminalServiceObject, delta.NewJsonObject(0))
		services := p2runtime.NativeOf[ServiceParts](self.Registry(contract.TerminalServices), "The terminal service")
		p2runtime.ContributeKeyed(self, sessioncontract.SessionArchived, p2runtime.Entry("terminals", func(ctx context.Context, session string) error {
			parts, err := services()
			if err == nil {
				var service *TerminalService
				if service, err = parts.Service(); err == nil {
					err = service.CloseSession(ctx, session)
				}
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "p2 terminals: closing session %s's terminals: %v\n", session, err)
			}
			return nil
		}))
		return nil
	}); err != nil {
		return err
	}
	if err := facet.Serve(contract.TerminalServiceObject, host.LocalFactory(serveService), host.ServeOptions{}); err != nil {
		return err
	}
	if err := facet.Serve(contract.Terminals, host.RemoteFactory(serveTerminals), host.ServeOptions{}); err != nil {
		return err
	}
	return facet.Serve(contract.AgentTerminals, host.RemoteFactory(serveAgentTerminals), host.ServeOptions{})
})

func serveService(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
	resource := self.Open(
		func(context.Context) (any, error) {
			info, err := server.Member("info").Value()
			if err != nil {
				return nil, err
			}
			return NewTerminalService(stringOf(jsonObject(info).Value("dataDir"))), nil
		},
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(*TerminalService).Close()
			return nil
		},
	)
	service := func() (*TerminalService, error) {
		value, err := resource.Value()
		if err != nil {
			return nil, err
		}
		return value.(*TerminalService), nil
	}
	store := self.(host.StoreSelf)
	store.Contribute(contract.TerminalServices, &ServiceParts{Service: service})
	p2runtime.ContributeKeyed(store, p2runtime.Upgrades, p2runtime.Entry("/terminal", p2runtime.UpgradeHandler(
		func(response http.ResponseWriter, request *http.Request, principal p2runtime.Principal, _ host.Ctx) error {
			if !p2runtime.IsOwner(principal) {
				response.Header().Set("Connection", "close")
				response.WriteHeader(http.StatusForbidden)
				return nil
			}
			terminals, err := service()
			if err != nil {
				return err
			}
			terminals.Upgrade(response, request)
			return nil
		})))
	return contracts.Implementation{}, nil
}

func serveTerminals(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	if who, err := p2runtime.FromChord(principal); err != nil || !p2runtime.IsOwner(who) {
		return contracts.Implementation{}, p2runtime.Forbidden("terminals are owner-only")
	}
	id := stringOf(params.Value("session"))
	self.Get(contract.TerminalServiceObject, delta.NewJsonObject(0))
	services := p2runtime.NativeOf[ServiceParts](self.Registry(contract.TerminalServices), "The terminal service")
	service := func() (*TerminalService, error) {
		parts, err := services()
		if err != nil {
			return nil, err
		}
		return parts.Service()
	}
	catalog := self.Get(sessioncontract.Catalog, delta.NewJsonObject(0))
	children := self.Get(subagents.ChildStore, delta.NewJsonObject(0))
	view := self.Open(
		func(ctx context.Context) (any, error) {
			// An unknown session refuses the view.
			if _, err := catalog.Call("get", delta.JsonObjectOf("id", id), ctx); err != nil {
				return nil, err
			}
			terminals, err := service()
			if err != nil {
				return nil, err
			}
			opened, err := terminals.Open(ctx, id)
			if err != nil {
				return nil, err
			}
			// The service is kept with the view: reading the native while the object closes would read a registry that is
			// composing.
			return &heldView{view: opened, service: terminals}, nil
		},
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			held := value.(*heldView)
			held.service.Release(held.view)
			return nil
		},
	)
	// Each call is one request of the terminal host's protocol, for this session's view.
	command := func(ctx context.Context, request TerminalRequest) (delta.JsonValue, error) {
		value, err := view.Value()
		if err != nil {
			return nil, err
		}
		return nil, value.(*heldView).view.Command(ctx, request)
	}
	return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
		"channel": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			value, err := view.Value()
			if err != nil {
				return nil, err
			}
			terminals, err := service()
			if err != nil {
				return nil, err
			}
			token, err := terminals.Channel(ctx, value.(*heldView).view)
			if err != nil {
				return nil, err
			}
			return delta.JsonObjectOf("token", token), nil
		},
		"list": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			return command(ctx, TerminalRequest{Type: RequestList, SessionID: id})
		},
		"open": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := jsonObject(args)
			session, err := catalog.Call("get", delta.JsonObjectOf("id", id), ctx)
			if err != nil {
				return nil, err
			}
			row := jsonObject(session)
			if row.Value("archivedAt") != nil {
				return nil, fmt.Errorf("Session is archived")
			}
			// Where the conversation works: a subagent's own location stays where it is when the session moves.
			conversation := in.Value("conversation")
			var child *delta.JsonObject
			list, err := childrenOf(children, id)
			if err != nil {
				return nil, err
			}
			for _, each := range list {
				if candidate := jsonObject(each); candidate.Value("conversationId") == conversation {
					child = candidate
					break
				}
			}
			if conversation != float64(1) && conversation != row.Value("main") && child == nil {
				return nil, fmt.Errorf("Unknown conversation")
			}
			location := subagents.ConversationLocation(row, child)
			var env *string
			if text, ok := location.Value("env").(string); ok {
				env = &text
			}
			return command(ctx, TerminalRequest{
				Type: RequestOpen, SessionID: id, Cwd: stringOf(location.Value("cwd")), Env: env,
				Cols: intOf(in.Value("cols")), Rows: intOf(in.Value("rows")),
			})
		},
		"attach": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			return command(ctx, TerminalRequest{Type: RequestAttach, TerminalID: stringOf(jsonObject(args).Value("terminalId"))})
		},
		"close": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			request := TerminalRequest{Type: RequestClose}
			if force, ok := jsonObject(args).Value("force").(bool); ok {
				request.Force = &force
			}
			return command(ctx, request)
		},
		"resize": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := jsonObject(args)
			return command(ctx, TerminalRequest{Type: RequestResize, Cols: intOf(in.Value("cols")), Rows: intOf(in.Value("rows"))})
		},
		"theme": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := jsonObject(args)
			return command(ctx, TerminalRequest{Type: RequestTheme, Theme: TerminalTheme{
				Foreground: intOf(in.Value("foreground")), Background: intOf(in.Value("background")), Scheme: stringOf(in.Value("scheme")),
			}})
		},
		"ack": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			return command(ctx, TerminalRequest{Type: RequestAck, Bytes: intOf(jsonObject(args).Value("bytes"))})
		},
		"restartHost": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			return command(ctx, TerminalRequest{Type: RequestShutdown})
		},
	}}}, nil
}

// childrenOf are the session's subagents the server recorded.
func childrenOf(store *host.Reference, session string) ([]any, error) {
	value, err := store.Member("children").Value()
	if err != nil {
		return nil, err
	}
	list, _ := jsonObject(value).Value(session).([]any)
	return list, nil
}

// currentWorker refuses all but the session's running current generation, as MCP's worker view does. A worker may ask
// before the server published its start: the check waits for that (the wait ends with the caller's call).
func currentWorker(hosts *host.Reference, principal p2runtime.WorkerPrincipal) func(ctx context.Context) error {
	known := func(state *delta.JsonObject) bool {
		if state == nil {
			return false
		}
		generation := intOf(state.Value("generation"))
		return generation > principal.Generation || (generation == principal.Generation && state.Value("running") == true)
	}
	return func(ctx context.Context) error {
		workers := hosts.Member("workers")
		published, err := workers.Value()
		if err != nil {
			return err
		}
		state := jsonObject(jsonObject(published).Value(principal.Session))
		if !known(state) {
			waited, err := p2runtime.Until(ctx, workers, func(value delta.JsonValue) bool {
				return known(jsonObject(jsonObject(value).Value(principal.Session)))
			})
			if err != nil {
				return err
			}
			state = jsonObject(jsonObject(waited).Value(principal.Session))
		}
		if state == nil || state.Value("running") != true || intOf(state.Value("generation")) != principal.Generation {
			return p2runtime.Forbidden("stale worker")
		}
		return nil
	}
}

func serveAgentTerminals(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	who, err := p2runtime.FromChord(principal)
	worker, ok := who.(p2runtime.WorkerPrincipal)
	if err != nil || !ok {
		return contracts.Implementation{}, p2runtime.Forbidden("agent terminals are for session workers")
	}
	self.Get(contract.TerminalServiceObject, delta.NewJsonObject(0))
	services := p2runtime.NativeOf[ServiceParts](self.Registry(contract.TerminalServices), "The terminal service")
	catalog := self.Get(sessioncontract.Catalog, delta.NewJsonObject(0))
	children := self.Get(subagents.ChildStore, delta.NewJsonObject(0))
	current := currentWorker(self.Get(p2runtime.Hosts, delta.NewJsonObject(0)), worker)
	sessionID := worker.Session
	// admitted is the session's live, current worker only, never in an archived session.
	admitted := func(ctx context.Context) (*delta.JsonObject, error) {
		if err := current(ctx); err != nil {
			return nil, err
		}
		session, err := catalog.Call("get", delta.JsonObjectOf("id", sessionID), ctx)
		if err != nil {
			return nil, err
		}
		if jsonObject(session).Value("archivedAt") != nil {
			return nil, p2runtime.Forbidden("Session is archived")
		}
		return jsonObject(session), nil
	}
	call := func(ctx context.Context, kind string, request HostCall) (HostResult, error) {
		if _, err := admitted(ctx); err != nil {
			return HostResult{}, err
		}
		parts, err := services()
		if err != nil {
			return HostResult{}, err
		}
		service, err := parts.Service()
		if err != nil {
			return HostResult{}, err
		}
		result, err := service.Call(ctx, request)
		if err != nil {
			return HostResult{}, err
		}
		if result.Kind != kind {
			return HostResult{}, fmt.Errorf("The terminal host answered %s to %s", result.Kind, request.Op)
		}
		return result, nil
	}
	reply := func(result HostResult, err error) (delta.JsonValue, error) {
		if err != nil {
			return nil, err
		}
		object := result.JSON()
		object.Delete("kind")
		return object, nil
	}
	return contracts.Implementation{Variant: "worker", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
		"list": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			return reply(call(ctx, KindList, HostCall{Op: OpList, SessionID: sessionID}))
		},
		"open": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := jsonObject(args)
			session, err := admitted(ctx)
			if err != nil {
				return nil, err
			}
			// Only where the session's conversations work: the session's environment or a subagent's own.
			own, err := childrenOf(children, sessionID)
			if err != nil {
				return nil, err
			}
			allowed := []any{session.Value("env")}
			for _, each := range own {
				allowed = append(allowed, subagents.ConversationLocation(session, jsonObject(each)).Value("env"))
			}
			location := jsonObject(in.Value("location"))
			wanted, wantedText := location.Value("env"), stringOf(location.Value("env"))
			found := false
			for _, env := range allowed {
				if env == nil {
					env = nil
				}
				found = found || env == wanted
			}
			if !found {
				where := "this device"
				if wantedText != "" {
					where = `"` + wantedText + `"`
				}
				return nil, p2runtime.Forbidden("This session does not work on " + where)
			}
			var env *string
			if text, ok := wanted.(string); ok {
				env = &text
			}
			opened, err := call(ctx, KindTerminal, HostCall{
				Op: OpOpen, SessionID: sessionID, Key: stringOf(in.Value("key")), Cwd: stringOf(location.Value("cwd")), Env: env,
				Name: stringOf(in.Value("name")), Command: stringOf(in.Value("command")), Cols: agentCols, Rows: agentRows,
			})
			if err != nil {
				return nil, err
			}
			return opened.Terminal.JSON(), nil
		},
		"send": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := jsonObject(args)
			var keys []string
			list, _ := in.Value("keys").([]any)
			for _, key := range list {
				keys = append(keys, stringOf(key))
			}
			sent, err := call(ctx, KindTerminal, HostCall{
				Op: OpSend, SessionID: sessionID, Key: stringOf(in.Value("key")), Target: stringOf(in.Value("target")),
				Keys: keys, Literal: in.Value("literal") == true,
			})
			if err != nil {
				return nil, err
			}
			return sent.Terminal.JSON(), nil
		},
		"cursor": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			return reply(call(ctx, KindCursor, HostCall{Op: OpCursor, SessionID: sessionID, Target: stringOf(jsonObject(args).Value("target"))}))
		},
		"read": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := jsonObject(args)
			request := HostCall{Op: OpRead, SessionID: sessionID, Target: stringOf(in.Value("target"))}
			if from, ok := in.Value("from").(float64); ok {
				offset := int64(from)
				request.From = &offset
			}
			if wait := jsonObject(in.Value("wait")); wait != nil {
				request.Wait = &TerminalWait{
					Until: stringOf(wait.Value("until")), IgnoreCase: wait.Value("ignoreCase") == true,
					QuietMs: intOf(wait.Value("quietMs")), TimeoutMs: intOf(wait.Value("timeoutMs")),
				}
				if pattern, ok := wait.Value("pattern").(string); ok {
					request.Wait.Pattern = &pattern
				}
			}
			return reply(call(ctx, KindRead, request))
		},
		"capture": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := jsonObject(args)
			return reply(call(ctx, KindCapture, HostCall{
				Op: OpCapture, SessionID: sessionID, Target: stringOf(in.Value("target")), Scrollback: intOf(in.Value("scrollback")),
				Escapes: in.Value("escapes") == true,
			}))
		},
		"close": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := jsonObject(args)
			return reply(call(ctx, KindClosed, HostCall{Op: OpClose, SessionID: sessionID, Key: stringOf(in.Value("key")), Target: stringOf(in.Value("target"))}))
		},
	}}}, nil
}

var _ chord.Watchable[delta.JsonValue]
