package locations

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/locations/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/locations/tool.ts: the location tools. An agent lists the environments it may work on and their
// directories, and moves itself. Every main agent has them, and the subagents and one-shot agents it gives them to. Only turns
// the owner started may use them (their `origin: "owner"`): guests can neither discover environments nor move agents.

// MaxListed is how many folders a listing shows.
const MaxListed = 200

// ExtensionName is the extension's name.
const ExtensionName = "p2-locations"

// EnvInfo is one environment as an agent sees it.
type EnvInfo struct {
	Name string
	// Type is "ssh", or "gondolin": a sandbox.
	Type string
	Host string
	User string
	Port *float64
	// Tested is the owner's last test of this configuration; nil when there is none.
	Tested *EnvTested
}

// EnvTested is the outcome of a test: the platform when it passed, the error when it failed.
type EnvTested struct {
	OK             bool
	Platform, Arch string
	Error          string
}

// Server is what the tools ask the server (`AgentLocations`).
type Server interface {
	Envs(ctx context.Context) ([]EnvInfo, error)
	Directories(ctx context.Context, env *string, path string) (p2runtime.DirectoryListing, error)
}

// Tools are what the location tools need: the server, and where a conversation works now.
type Tools struct {
	Server Server
	Locate func(ctx context.Context, id durable.ConversationId, read durable.DocumentReader) (p2runtime.Location, error)
}

// SandboxAt is the sandbox a conversation working at here is confined to, decided by its environment's configured type: it
// may move within the sandbox, never out of it (only the owner's Relocate leaves it). "" when it is not in one.
func SandboxAt(here p2runtime.Location, envs []EnvInfo) string {
	if here.Env != nil && slices.ContainsFunc(envs, func(env EnvInfo) bool { return env.Name == *here.Env && env.Type == "gondolin" }) {
		return *here.Env
	}
	return ""
}

func describeEnv(env EnvInfo, here p2runtime.Location) string {
	target := "ssh "
	if env.Type == "gondolin" {
		target = "sandbox (a VM on " + env.Host + ")"
	} else {
		if env.User != "" {
			target += env.User + "@"
		}
		target += env.Host
		if env.Port != nil && *env.Port != 0 {
			target += fmt.Sprintf(":%d", int(*env.Port))
		}
	}
	tested := "not tested"
	switch {
	case env.Tested != nil && env.Tested.OK:
		tested = "tested ok (" + env.Tested.Platform + " " + env.Tested.Arch + ")"
	case env.Tested != nil:
		tested = "last test failed: " + env.Tested.Error
	}
	line := "- " + env.Name + ": " + target + " · " + tested
	if here.Env != nil && *here.Env == env.Name {
		line += " · you are here"
	}
	return line
}

// where are the parameters that name a place.
func where() schema.Properties {
	return schema.Properties{
		schema.Opt("location", schema.Str(schema.Description("[env:]path, e.g. gpu:~/project, local:/tmp (this device) or ../other (relative to your cwd); no prefix: your environment"))),
		schema.Opt("env", schema.Union([]schema.Schema{schema.Str(), schema.Nul()}, schema.Description("Instead of location: an environment, null for this device"))),
		schema.Opt("cwd", schema.Str(schema.Description("Instead of location: a path on env (default your environment)"))),
	}
}

// args are the parameters of a tool that names a place.
type args struct {
	location, cwd *string
	// env is set when the call gave it; envNull when it gave null (this device).
	env     *string
	envSet  bool
	envNull bool
}

func argsOf(value any) args {
	fields, _ := value.(map[string]any)
	var out args
	if text, ok := fields["location"].(string); ok {
		out.location = &text
	}
	if text, ok := fields["cwd"].(string); ok {
		out.cwd = &text
	}
	if raw, present := fields["env"]; present {
		out.envSet = true
		if text, ok := raw.(string); ok {
			out.env = &text
		} else {
			out.envNull = true
		}
	}
	return out
}

func (a args) none() bool { return a.location == nil && a.cwd == nil && !a.envSet }

func sameEnv(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

type found struct {
	listing p2runtime.DirectoryListing
	env     *string
}

// CreateExtension is the location tools' extension (`createLocationExtension`).
func CreateExtension(tools Tools) (*p2runtime.P2Extension, error) {
	// target is `[env:]path` (or `env` and `cwd`) from where the caller works, resolved by the server to an existing
	// directory: the place, or why not (a message for the model).
	target := func(ctx context.Context, a args, here p2runtime.Location) (*found, string, error) {
		envs, err := tools.Server.Envs(ctx)
		if err != nil {
			return nil, "", err
		}
		names := make([]string, len(envs))
		for at, env := range envs {
			names[at] = env.Name
		}
		sandbox := SandboxAt(here, envs)
		var parsed p2runtime.LocationInput
		if a.location != nil {
			parsed, err = p2runtime.ParseLocation(*a.location, names, p2runtime.LocationFallback{Env: here.Env, Cwd: here.Cwd})
		} else {
			fallback := p2runtime.LocationFallback{Env: here.Env}
			switch {
			case a.envNull:
				fallback.Env = nil
			case a.env != nil:
				fallback.Env = a.env
			}
			if !a.envSet || sameEnv(fallback.Env, here.Env) {
				fallback.Cwd = here.Cwd
			}
			cwd := ""
			if a.cwd != nil {
				cwd = *a.cwd
			}
			parsed, err = p2runtime.ParseLocation(cwd, names, fallback)
		}
		if err != nil {
			return nil, err.Error(), nil
		}
		if sandbox != "" && (parsed.Env == nil || *parsed.Env != sandbox) {
			return nil, fmt.Sprintf("You work in sandbox %s and cannot leave it; use paths inside %s.", sandbox, sandbox), nil
		}
		if parsed.Env != nil && !slices.Contains(names, *parsed.Env) {
			return nil, fmt.Sprintf("Unknown environment %q (location_envs lists them).", *parsed.Env), nil
		}
		listing, err := tools.Server.Directories(ctx, parsed.Env, parsed.Path)
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", err
			}
			place := parsed.Path
			if parsed.Env != nil {
				place = *parsed.Env + ":" + place
			}
			return nil, place + ": " + err.Error(), nil
		}
		return &found{listing: listing, env: parsed.Env}, "", nil
	}
	reply := func(text string, details delta.JsonValue, isError bool) durable.ToolExecutionResult {
		return p2runtime.ToolText(text, details, isError)
	}
	owner := p2runtime.ToolAccess{Origin: "owner"}
	tool := func(name, description string, parameters schema.Schema, execute func(ctx context.Context, a any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error)) (p2runtime.P2Tool, error) {
		toolSchema, err := p2runtime.ToolSchemaOf(name, description, parameters)
		if err != nil {
			return p2runtime.P2Tool{}, err
		}
		return p2runtime.P2Tool{
			ToolRegistration: harness.DefineTool(durable.ToolRegistration{
				ToolSchema: toolSchema,
				// Setting the same destination again changes nothing.
				Replay:  durable.ReplaySafe,
				Execute: execute,
			}),
			ToolAccess: owner,
		}, nil
	}

	envsTool, err := tool("location_envs",
		"List the environments you can work on: this device, the configured SSH environments (host, last test) and sandboxes. Shows where you work now.",
		schema.Obj(schema.Properties{}, schema.Open()),
		func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			here, err := tools.Locate(ctx, api.ConversationId(), api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			envs, err := tools.Server.Envs(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if sandbox := SandboxAt(here, envs); sandbox != "" {
				lines := []string{
					"You work at " + p2runtime.LocationText(here) + ", in sandbox " + sandbox + ": a VM whose tools reach only its own files and allowed hosts.",
					"You cannot leave it; location_dirs and location_move take paths inside it.",
				}
				for _, env := range envs {
					if env.Name == sandbox {
						lines = append(lines, describeEnv(env, here))
					}
				}
				return reply(strings.Join(lines, "\n"), delta.NewJsonObject(0), false), nil
			}
			// The session's move is kept on conversation 1 whichever top-level conversation asked for it.
			var move *contract.MoveState
			top, err := p2runtime.IsTopLevel(ctx, api, api.ConversationId())
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if top {
				if move, err = durable.Snapshot(ctx, api, contract.LocationMoveDoc, durable.ROOT_CONVERSATION_ID); err != nil {
					return durable.ToolExecutionResult{}, err
				}
			}
			lines := []string{"You work at " + p2runtime.LocationText(here) + "."}
			if move != nil && move.Pending != nil {
				lines = append(lines, "Your session moves to "+p2runtime.LocationText(*subagentscontract.LocationOf(move.Pending))+" when this turn ends.")
			}
			if move != nil && move.Failed != nil {
				lines = append(lines, "The last move to "+p2runtime.LocationText(*subagentscontract.LocationOf(&move.Failed.To))+" failed: "+move.Failed.Error)
			}
			local := "- local: this device (where p2 runs)"
			if here.Env == nil {
				local += " · you are here"
			}
			lines = append(lines, "Environments:", local)
			for _, env := range envs {
				lines = append(lines, describeEnv(env, here))
			}
			return reply(strings.Join(lines, "\n"), delta.NewJsonObject(0), false), nil
		})
	if err != nil {
		return nil, err
	}

	dirsTool, err := tool("location_dirs",
		"List the folders of a directory on an environment (default: your cwd), to find where to work. Shows at most 200.",
		schema.Obj(where(), schema.Open()),
		func(ctx context.Context, raw any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			here, err := tools.Locate(ctx, api.ConversationId(), api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			a := argsOf(raw)
			if a.none() {
				a = args{location: &here.Cwd}
			}
			place, problem, err := target(ctx, a, here)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if place == nil {
				return reply(problem, delta.NewJsonObject(0), true), nil
			}
			prefix := ""
			if place.env != nil {
				prefix = *place.env + ":"
			}
			listing := place.listing
			shown := listing.Directories[:min(len(listing.Directories), MaxListed)]
			names := make([]string, len(shown))
			for at, path := range shown {
				names[at] = lastSegment(path) + "/"
			}
			more := len(listing.Directories) - len(names)
			head := prefix + listing.Path
			if listing.Parent != nil {
				head += " (parent " + prefix + *listing.Parent + ")"
			}
			lines := []string{head, "No folders."}
			if len(names) > 0 {
				lines[1] = strings.Join(names, "\n")
			}
			if more > 0 || listing.Truncated {
				note := "More folders are not shown"
				if more > 0 {
					note += fmt.Sprintf(" (%d+)", more)
				}
				lines = append(lines, note+".")
			}
			return reply(strings.Join(lines, "\n"), delta.NewJsonObject(0), false), nil
		})
	if err != nil {
		return nil, err
	}

	moveTool, err := tool("location_move", strings.Join([]string{
		"Move yourself to another directory or environment.",
		"The main agent moves its whole session there once this turn has ended (after your last answer, when no subagent",
		"work is running); subagents following the session move with it, subagents with their own location stay. Your",
		"tools keep running where you are until then.",
		"A subagent given this tool moves only itself, at once: its next tool calls run at the new location.",
	}, "\n"), schema.Obj(where(), schema.Open()),
		func(ctx context.Context, raw any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			a := argsOf(raw)
			if a.none() {
				return reply("location_move needs location, or env and cwd.", delta.NewJsonObject(0), true), nil
			}
			here, err := tools.Locate(ctx, api.ConversationId(), api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			place, problem, err := target(ctx, a, here)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if place == nil {
				return reply(problem, delta.NewJsonObject(0), true), nil
			}
			to := p2runtime.Location{Env: place.env, Cwd: place.listing.Path}
			var env any
			if to.Env != nil {
				env = *to.Env
			}
			details := delta.JsonObjectOf("env", env, "cwd", to.Cwd)
			top, err := p2runtime.IsTopLevel(ctx, api, api.ConversationId())
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if top {
				state, err := durable.Snapshot(ctx, api, contract.LocationMoveDoc, durable.ROOT_CONVERSATION_ID)
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				if p2runtime.SameLocation(to, here) && (state == nil || state.Pending == nil) {
					return reply("You already work at "+p2runtime.LocationText(to)+".", details, false), nil
				}
				if _, err := durable.Commit(ctx, api, func(tx durable.Tx) (struct{}, error) {
					draft, err := durable.TxDoc(tx, contract.LocationMoveDoc, durable.ROOT_CONVERSATION_ID)
					if err != nil {
						return struct{}{}, err
					}
					draft.Delete("failed")
					return struct{}{}, draft.Set("pending", delta.JsonObjectOf("cwd", to.Cwd, "env", env))
				}); err != nil {
					return durable.ToolExecutionResult{}, err
				}
				return reply("The session moves to "+p2runtime.LocationText(to)+" when this turn ends (once no run or subagent work is active). Subagents following the session move with it; subagents with their own location stay where they are.", details, false), nil
			}
			if p2runtime.SameLocation(to, here) {
				return reply("You already work at "+p2runtime.LocationText(to)+".", details, false), nil
			}
			moved, err := durable.Commit(ctx, api, func(tx durable.Tx) (bool, error) {
				return subagentscontract.PlaceSubagent(tx, api.ConversationId(), to)
			})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if !moved {
				return reply("Only the main agent and subagents can move.", delta.NewJsonObject(0), true), nil
			}
			return reply("You work at "+p2runtime.LocationText(to)+" now; the session's moves no longer move you.", details, false), nil
		})
	if err != nil {
		return nil, err
	}

	return &p2runtime.P2Extension{
		Extension: &durable.Extension{Name: ExtensionName},
		Tools:     []p2runtime.P2Tool{envsTool, dirsTool, moveTool},
	}, nil
}

// lastSegment is the last name of a path, as `path.split(/[\\/]/).pop()`.
func lastSegment(path string) string {
	return path[strings.LastIndexAny(path, `/\`)+1:]
}
