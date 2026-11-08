package conversations

import (
	"context"
	"errors"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/conversations/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/plugins/conversations/server.ts: the server's side of peer conversations: the broker between session workers
// (a local object, so its references act as the server, whom `PeerConversation` serves), and the workers' view of it, fenced
// by worker generation.

// listLimit is how many sessions a list shows.
const listLimit = 200

func object(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func text(value any) string {
	text, _ := value.(string)
	return text
}

// broker is the ConversationBroker's `query`.
func broker(self host.Self) contracts.MethodFunction {
	catalog := self.Get(sessionscontract.Catalog, delta.NewJsonObject(0))
	statuses := self.Get(statuscontract.StatusStore, delta.NewJsonObject(0))
	return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
		in := object(args)
		source, requestID, origin, query := text(in.Value("source")), text(in.Value("requestId")), in.Value("origin"), object(in.Value("query"))
		sender, err := catalog.Call("get", delta.JsonObjectOf("id", source), ctx)
		if err != nil {
			return nil, err
		}
		if object(sender).Value("archivedAt") != nil {
			return nil, p2runtime.Forbidden("The sending session is archived")
		}
		action := text(query.Value("action"))
		if action == "list" {
			rows, err := catalog.Member("sessions").Value()
			if err != nil {
				return nil, err
			}
			var sessions []*delta.JsonObject
			list, _ := rows.([]any)
			for _, row := range list {
				if object(row).Value("archivedAt") == nil {
					sessions = append(sessions, object(row))
				}
			}
			states, err := statuses.Member("statuses").Value()
			if err != nil {
				return nil, err
			}
			peers := make([]any, 0, min(len(sessions), listLimit))
			for _, session := range sessions[:min(len(sessions), listLimit)] {
				state := "idle"
				status := object(object(states).Value(text(session.Value("id"))))
				switch {
				case status == nil || status.Value("running") != true:
					state = "stopped"
				default:
					busy, _ := status.Value("busy").([]any)
					for _, conversation := range busy {
						if conversation == float64(1) {
							state = "working"
						}
					}
				}
				model := object(session.Value("model"))
				peers = append(peers, delta.JsonObjectOf(
					"id", session.Value("id"), "title", session.Value("title"),
					"model", text(model.Value("provider"))+"/"+text(model.Value("modelId")), "state", state,
				))
			}
			return delta.JsonObjectOf("action", "list", "self", source, "truncated", len(sessions) > listLimit, "peers", peers), nil
		}
		to := text(query.Value("to"))
		if action == "send" && to == source {
			return nil, errors.New("Cannot send a message to yourself")
		}
		recipient, err := catalog.Call("get", delta.JsonObjectOf("id", to), ctx)
		if err != nil {
			return nil, err
		}
		if object(recipient).Value("archivedAt") != nil {
			return nil, errors.New("The destination is archived")
		}
		message := text(query.Value("message"))
		if action == "send" && strings.TrimFunc(message, isJSSpace) == "" {
			return nil, errors.New("send needs a message")
		}
		recipientID := text(object(recipient).Value("id"))
		archived := func() (bool, error) {
			for _, id := range []string{source, recipientID} {
				row, err := catalog.Call("get", delta.JsonObjectOf("id", id), ctx)
				if err != nil {
					return false, err
				}
				if object(row).Value("archivedAt") != nil {
					return true, nil
				}
			}
			return false, nil
		}
		// Each attempt holds the recipient's worker afresh, starting it if needed, as a browser opening it does.
		deliver := func() (delta.JsonValue, error) {
			peer, err := p2runtime.CtxOf(ctx).Get(contract.PeerConversation, delta.JsonObjectOf("session", recipientID), host.GetOptions{})
			if err != nil {
				return nil, err
			}
			// Startup can yield to archive/restore. Recheck both sessions before admitting peer work.
			if gone, err := archived(); err != nil {
				return nil, err
			} else if gone {
				return nil, p2runtime.Forbidden("Conversation is archived")
			}
			if action == "answer" {
				return peer.Call("answer", nil, ctx)
			}
			send := delta.JsonObjectOf("source", source, "title", object(sender).Value("title"), "requestId", requestID, "message", message)
			if followUp, ok := query.Value("followUp").(bool); ok {
				send.Set("followUp", followUp)
			}
			if origin != nil {
				send.Set("origin", origin)
			}
			return peer.Call("send", send, ctx)
		}
		delivered, err := deliver()
		if err != nil {
			// The worker went away under the reference (killed, stopped idle, paused): retry once with a fresh reference. The
			// worker deduplicates a send by its request ID, so a send that did arrive is not repeated.
			var failure *data.ChordError
			if ctx.Err() != nil || !errors.As(err, &failure) || (failure.Code != data.CodeUnavailable && failure.Code != data.CodeGone) {
				return nil, err
			}
			if delivered, err = deliver(); err != nil {
				return nil, err
			}
		}
		if action == "answer" {
			if gone, err := archived(); err != nil {
				return nil, err
			} else if gone {
				return nil, p2runtime.Forbidden("Conversation is archived")
			}
			reply := delta.JsonObjectOf("action", "answer", "to", recipientID)
			for key, value := range object(delivered).All() {
				reply.Set(key, value)
			}
			return reply, nil
		}
		if gone, err := archived(); err != nil {
			return nil, err
		} else if gone {
			return nil, p2runtime.Forbidden("Conversation is archived; a sent message may already be delivered")
		}
		return delta.JsonObjectOf("action", "send", "to", recipientID, "submissionId", object(delivered).Value("submissionId")), nil
	}
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// Server is the conversations plugin's server facet.
var Server = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := facet.Serve(contract.ConversationBroker, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{"query": broker(self)}}}, nil
	}), host.ServeOptions{}); err != nil {
		return err
	}
	return facet.Serve(contract.Conversations, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		worker, ok := who.(p2runtime.WorkerPrincipal)
		if err != nil || !ok {
			return contracts.Implementation{}, p2runtime.Forbidden("conversation messaging is for session workers")
		}
		brokerRef := self.Get(contract.ConversationBroker, delta.NewJsonObject(0))
		hosts := self.Get(p2runtime.Hosts, delta.NewJsonObject(0))
		return contracts.Implementation{Variant: "worker", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"query": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := object(args)
				workers, err := hosts.Member("workers").Value()
				if err != nil {
					return nil, err
				}
				current := object(object(workers).Value(worker.Session))
				if current == nil || current.Value("running") != true || current.Value("generation") != float64(worker.Generation) {
					return nil, p2runtime.Forbidden("stale worker")
				}
				forward := delta.JsonObjectOf("source", worker.Session, "requestId", in.Value("requestId"))
				if origin := in.Value("origin"); origin != nil {
					forward.Set("origin", origin)
				}
				forward.Set("query", in.Value("query"))
				return brokerRef.Call("query", forward, ctx)
			},
		}}}, nil
	}), host.ServeOptions{})
})
