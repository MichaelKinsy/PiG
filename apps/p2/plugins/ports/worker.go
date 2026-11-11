package ports

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/ports/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/ports/worker.ts and tool.ts: the worker facet contributes the `ports` tool, which asks the server
// (`Ports`, the worker's variant) to list, open and close forwards.

var workerFacet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.port-tools", registerPortTools)
})

const extensionName = "p2-ports"

const sectionText = "The ports tool forwards a local web server (a dev server, say) through Radius so the owner can open it from other devices, and lists all forwards and public endpoints. Forwards are private by default: only the owner can open them, after signing in. Never make a port public, by opening it with public: true or switching an existing one, without asking the user first and getting a yes: a public port is reachable by anyone with the link, without signing in."

var toolDescription = strings.Join([]string{
	"Forward web servers on this device through Radius, so the owner can open them from anywhere.",
	"list: every forward with its addresses and who opened it, and all public endpoints.",
	"open (port; optional name, public): forward a port and return its address. Private by default (owner only, after signing in). public: true also opens it to anyone with the link, without signing in; ask the user before doing that. Opening a port that is already forwarded, by anyone, keeps its address and updates its name and visibility.",
	"close (port): stop forwarding a port, whoever opened it.",
	"Only ports of this device: a session on a remote environment cannot forward its host's ports yet. A session's forwards close when it is archived.",
}, "\n")

// query is a request of the tool to the server (`PortsRequest`), as the JSON the contract carries.
type query = *delta.JsonObject

func registerPortTools(self host.StoreSelf) error {
	worker := p2runtime.WorkerOf(self)
	tool, err := portsTool(
		func(request query, ctx context.Context) (*delta.JsonObject, error) {
			parts, err := worker()
			if err != nil {
				return nil, err
			}
			answered, err := parts.Root(func(root host.Ctx) (any, error) {
				ports, err := root.Get(contract.Ports, delta.NewJsonObject(0), host.GetOptions{})
				if err != nil {
					return nil, err
				}
				return ports.Call("query", delta.JsonObjectOf("conversation", float64(1), "query", request), root)
			}, ctx)
			if err != nil {
				return nil, err
			}
			reply, _ := answered.(*delta.JsonObject)
			return reply, nil
		},
		func() string {
			parts, err := worker()
			if err != nil {
				return ""
			}
			session, err := parts.Init.Session()
			if err != nil {
				return ""
			}
			return session.ID
		},
	)
	if err != nil {
		return err
	}
	extension := &p2runtime.P2Extension{
		Extension: &durable.Extension{Name: extensionName, Sections: []*durable.PromptSection{portsSection()}},
		Tools:     []p2runtime.P2Tool{tool},
	}
	p2runtime.ContributeKeyed(self, p2runtime.Extensions, p2runtime.Entry(extensionName, extension))
	return nil
}

// portsSection is the system prompt's paragraph on the tool, in top-level conversations only.
func portsSection() *durable.PromptSection {
	return harness.Section("ports", func(ctx context.Context, input durable.PromptInput) (*string, error) {
		top, err := p2runtime.IsTopLevel(ctx, input.Read, input.ConversationId)
		if err != nil || !top {
			return nil, err
		}
		text := sectionText
		return &text, nil
	})
}

func textResult(text string, details *delta.JsonObject, isError bool) durable.ToolExecutionResult {
	result := durable.ToolExecutionResult{
		Output:     []ai.ToolResultMessageContent{ai.TextContent{Text: text}},
		Details:    details,
		HasDetails: true,
	}
	if isError {
		result.IsError = &isError
	}
	return result
}

// jsonQuote is `JSON.stringify(text)`.
func jsonQuote(text string) string {
	quoted, _ := chordjson.Marshal(text)
	return string(quoted)
}

// describe is what the agent learns: every forward, its addresses, who opened it, and the public endpoints spelled out.
func describe(reply *delta.JsonObject, self string) string {
	forwards, _ := reply.Value("forwards").([]any)
	label := func(forward *delta.JsonObject) string {
		port, _ := forward.Value("port").(float64)
		text := fmt.Sprintf("port %d", int(port))
		if name := stringOf(forward.Value("name")); name != "" {
			text += " (" + jsonQuote(name) + ")"
		}
		return text
	}
	opener := func(forward *delta.JsonObject) string {
		session := stringOf(forward.Value("session"))
		switch {
		case session == self:
			return "opened by this session"
		case session != "":
			title := stringOf(forward.Value("sessionTitle"))
			if title == "" {
				title = session
			}
			return "opened by session " + jsonQuote(title)
		}
		return "added by the owner"
	}
	var lines, publicOnes []string
	for _, each := range forwards {
		forward := objectOf(each)
		visibility := "private"
		if forward.Value("public") == true {
			visibility = "PUBLIC"
		}
		address := stringOf(forward.Value("url"))
		if address == "" {
			address = "address not known yet"
		}
		line := fmt.Sprintf("- %s, %s, %s: %s", label(forward), visibility, opener(forward), address)
		if publicURL := stringOf(forward.Value("publicUrl")); publicURL != "" {
			line += ", public " + publicURL
		}
		lines = append(lines, line)
		if forward.Value("public") == true {
			publicURL := stringOf(forward.Value("publicUrl"))
			if publicURL == "" {
				publicURL = "(address not known yet)"
			}
			publicOnes = append(publicOnes, label(forward)+" "+publicURL)
		}
	}
	out := []string{"No ports are forwarded."}
	if len(forwards) > 0 {
		out = []string{"Forwarded ports:"}
	}
	out = append(out, lines...)
	if len(publicOnes) > 0 {
		out = append(out, "Public endpoints, open to anyone with the link without signing in: "+strings.Join(publicOnes, "; ")+".")
	} else {
		out = append(out, "No forward is public.")
	}
	if reply.Value("addressesKnown") != true {
		out = append(out, "Addresses are not known yet: Pi has not been opened through Radius by its owner. They appear once it has; the owner also finds them under Envs → Ports.")
	}
	return strings.Join(out, "\n")
}

// portsTool is the `ports` tool: queryServer asks the server for a request, self is this session's ID.
func portsTool(queryServer func(query, context.Context) (*delta.JsonObject, error), self func() string) (p2runtime.P2Tool, error) {
	parameters := schema.Obj(schema.Properties{
		schema.Prop("action", schema.Union([]schema.Schema{p2runtime.Literal("list"), p2runtime.Literal("open"), p2runtime.Literal("close")})),
		schema.Opt("port", schema.Int(schema.Minimum(1), schema.Maximum(65535))),
		schema.Opt("name", schema.Str(schema.MinLength(1), schema.MaxLength(contract.MaxForwardName))),
		schema.Opt("public", schema.Bool()),
	}, schema.Open())
	encoded, err := json.Marshal(struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}{"ports", toolDescription, mustMarshal(parameters)})
	if err != nil {
		return p2runtime.P2Tool{}, err
	}
	var toolSchema ai.ToolSchema
	//perf:ok the tool schema is built once per worker start (cold)
	if err := json.Unmarshal(encoded, &toolSchema); err != nil { //portlint:allow jsonclone a typed value converted from its stored JSON form, cold (see the perf:ok above)
		return p2runtime.P2Tool{}, err
	}
	registration := harness.DefineTool(durable.ToolRegistration{
		ToolSchema: toolSchema,
		// Every action is idempotent: opening twice returns the same forward, closing twice reports it closed.
		Replay: durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			fields, _ := args.(map[string]any)
			action, _ := fields["action"].(string)
			port, hasPort := fields["port"].(float64)
			reply := func(text string, details *delta.JsonObject, isError bool) durable.ToolExecutionResult {
				full := delta.JsonObjectOf("action", action)
				if hasPort {
					full.Set("port", port)
				}
				for key, value := range details.All() {
					full.Set(key, value)
				}
				return textResult(text, full, isError)
			}
			var request query
			switch {
			case action == "list":
				request = delta.JsonObjectOf("action", "list")
			case !hasPort:
				return reply(action+" needs a port.", delta.NewJsonObject(0), true), nil
			case action == "open":
				request = delta.JsonObjectOf("action", "open", "port", port)
				if name, ok := fields["name"]; ok {
					request.Set("name", name)
				}
				if public, ok := fields["public"]; ok {
					request.Set("public", public)
				}
			default:
				request = delta.JsonObjectOf("action", "close", "port", port)
			}
			result, err := queryServer(request, ctx)
			if err != nil {
				if ctx.Err() != nil {
					return durable.ToolExecutionResult{}, err
				}
				return reply(err.Error(), delta.NewJsonObject(0), true), nil
			}
			var summary string
			forward := objectOf(result.Value("forward"))
			switch {
			case action == "open" && forward != nil:
				mode := ", private"
				if forward.Value("public") == true {
					mode = ", public"
				}
				address := stringOf(forward.Value("url"))
				if address == "" {
					address = "address not known yet"
				}
				summary = fmt.Sprintf("Forwarded port %d%s: %s", int(forward.Value("port").(float64)), mode, address)
				if publicURL := stringOf(forward.Value("publicUrl")); publicURL != "" {
					summary += " (public: " + publicURL + ")"
				}
				summary += "."
			case action == "close" && result.Value("closed") == true:
				summary = fmt.Sprintf("Stopped forwarding port %d.", int(port))
			case action == "close":
				summary = fmt.Sprintf("Port %d was not forwarded.", int(port))
			}
			parts := []string{}
			if summary != "" {
				parts = append(parts, summary)
			}
			parts = append(parts, describe(result, self()))
			details := delta.NewJsonObject(0)
			if forward != nil {
				details.Set("forward", forward)
			}
			return reply(strings.Join(parts, "\n\n"), details, false), nil
		},
	})
	return p2runtime.P2Tool{ToolRegistration: registration, ToolAccess: p2runtime.ToolAccess{Origin: "owner", Scope: "main", Locked: "ports"}}, nil
}

func mustMarshal(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
