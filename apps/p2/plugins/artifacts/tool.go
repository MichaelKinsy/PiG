package artifacts

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/artifacts/tool.ts: the artifact index document and the `artifact` tool.

// ArtifactEntry is one version in the index (`ArtifactInfo` as the document stores it).
type ArtifactEntry struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Type    string `json:"type"`
	Version int    `json:"version"`
}

// ArtifactState is the `p2.artifacts` document.
type ArtifactState struct {
	Artifacts []ArtifactEntry `json:"artifacts"`
}

// ArtifactDoc is the complete artifact index of one agent (on conversation 1 for the main agent, see
// `p2runtime.AgentConversation`); reset and compaction never rewind it.
var ArtifactDoc = durable.DefineDoc(durable.DocDefinition[ArtifactState]{
	CommonDocDefinition: durable.CommonDocDefinition[ArtifactState]{
		Kind:    "p2.artifacts",
		Version: 1,
		Initial: func() ArtifactState { return ArtifactState{Artifacts: []ArtifactEntry{}} },
		CheckpointWhen: func(_ ArtifactState, _ []durable.Op, info durable.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 64
		},
	},
	// A latest-only document cannot copy state as of a historical fork point. Start forks and independently created
	// subagents with their own empty index.
	DocumentSemantics: durable.NewLatestConversationSemantics(durable.ForkInitial).DocumentSemantics,
})

// SaveArtifactFunc saves the next version of an artifact for the agent of conversation holder (the session's files).
type SaveArtifactFunc func(input ArtifactInput, holder int, ctx context.Context) (p2runtime.ArtifactInfo, error)

func infoJSON(info p2runtime.ArtifactInfo) *delta.JsonObject {
	return delta.JsonObjectOf("name", info.Name, "title", info.Title, "type", string(info.Type), "version", float64(info.Version))
}

// toolParameters are the `artifact` tool's parameters.
func toolParameters() schema.Schema {
	return schema.Obj(schema.Properties{
		schema.Prop("name", schema.Str(schema.Pattern("^[a-z0-9][a-z0-9_-]{0,63}$"), schema.MinLength(1), schema.MaxLength(64))),
		schema.Prop("title", schema.Str(schema.MinLength(1), schema.MaxLength(200))),
		schema.Prop("type", schema.Union([]schema.Schema{
			p2runtime.Literal("html"), p2runtime.Literal("svg"), p2runtime.Literal("markdown"), p2runtime.Literal("text"),
		})),
		schema.Prop("content", schema.Str()),
		schema.Opt("inline", schema.Bool(schema.Description(
			"Also render it inline at the tool call site in the transcript (default false). For diagrams and small visual results."))),
	})
}

// artifactTool is the `artifact` tool, saving through save.
func artifactTool(save SaveArtifactFunc) (*durable.ToolRegistration, error) {
	tool, err := p2runtime.ToolSchemaOf("artifact", toolDescription, toolParameters())
	if err != nil {
		return nil, err
	}
	return harness.DefineTool(durable.ToolRegistration{
		ToolSchema: tool,
		Replay:     durable.ReplayUnsafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			fields, _ := args.(map[string]any)
			text := func(key string) string { value, _ := fields[key].(string); return value }
			inline, _ := fields["inline"].(bool)
			input := ArtifactInput{Name: text("name"), Title: text("title"), Type: p2runtime.ArtifactType(text("type")), Content: text("content")}
			// The main agent's list is one, whichever `/tree` branch it works in.
			holder, err := p2runtime.AgentConversation(ctx, api, api.ConversationId())
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			info, err := save(input, int(holder), ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			entry := infoJSON(info)
			if _, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
				draft, err := durable.TxDoc[ArtifactState](tx, ArtifactDoc, holder)
				if err != nil {
					return nil, err
				}
				state, err := durable.FromJsonValue[ArtifactState](draft.Snapshot())
				if err != nil {
					return nil, err
				}
				for _, known := range state.Artifacts {
					if known.Name == info.Name && known.Version == info.Version {
						return nil, nil
					}
				}
				_, err = draft.Array("artifacts").Push(entry)
				return nil, err
			}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			ready := fmt.Sprintf("Artifact ready: %s (%s, version %d).", info.Title, info.Name, info.Version)
			details := infoJSON(info)
			if inline {
				ready += " Shown inline in the transcript."
				details.Set("inline", true)
			}
			return durable.ToolExecutionResult{
				Output:     []ai.ToolResultMessageContent{ai.TextContent{Text: ready}},
				Details:    details,
				HasDetails: true,
			}, nil
		},
	}), nil
}

// extensionName is the extension that carries the tool.
const extensionName = "p2-artifacts"

// CreateArtifactExtension is the extension with the `artifact` tool, saving through save.
func CreateArtifactExtension(save SaveArtifactFunc) (*p2runtime.P2Extension, error) {
	tool, err := artifactTool(save)
	if err != nil {
		return nil, err
	}
	return &p2runtime.P2Extension{
		Extension: &durable.Extension{Name: extensionName},
		Tools:     []p2runtime.P2Tool{{ToolRegistration: tool}},
	}, nil
}
