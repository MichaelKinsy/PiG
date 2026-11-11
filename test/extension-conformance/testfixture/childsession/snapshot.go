package childsession

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type snapshotFields struct {
	id, file, prompt, thinking string
	model                      map[string]any
	tools                      []string
}

// readSnapshot reads the deprecated creation-time fields, which is the point of the scenario.
//
//nolint:staticcheck // SA1019: these fields are deprecated and this scenario proves they still hold the creation values.
func readSnapshot(s *sdk.AgentSession) snapshotFields {
	//lint:ignore SA1019 tests the deprecated snapshot fields for compatibility
	return snapshotFields{s.SessionID, s.SessionFile, s.SystemPrompt, s.ThinkingLevel, s.Model, s.ActiveTools}
}

// registerSnapshot is the child_snapshot scenario: the deprecated creation-time fields of an AgentSession beside the live Current* methods. The session is file-backed with one active tool; the fields hold what it had when CreateAgentSession returned, the methods read what it holds now, and the observations differ after the active tools change (a snapshot that followed the session, or a method that returned the creation values, fails).
func registerSnapshot(e *sdk.Extension) {
	e.RegisterTool(sdk.ToolDefinition{
		Name: "child_snapshot", Label: "child_snapshot", Description: "child_snapshot", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (any, error) {
			dir := args["dir"].(string)
			session, err := ctx.CreateAgentSession(sdk.CreateAgentSessionOptions{
				Cwd: dir, AgentDir: filepath.Join(dir, "agent"), Model: map[string]any{"provider": "sentinel", "id": "echo"}, Tools: []string{"sentinel_tool"},
				Settings:  map[string]any{"compaction": map[string]any{"enabled": false}},
				Resources: sdk.ResourceOptions{NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true},
				CustomTools: []sdk.ToolDefinition{{
					Name: "sentinel_tool", Label: "sentinel", Description: "uppercases x", PromptSnippet: "sentinel-snippet", Parameters: sdk.Schema{"type": "object"},
					Execute: func(sdk.Context, map[string]any) (any, error) { return "", nil },
				}},
			})
			if err != nil {
				return nil, err
			}
			defer session.Dispose()
			var failure error
			ok := func(value any, err error) any {
				if err != nil && failure == nil {
					failure = err
				}
				return value
			}
			out := map[string]any{}
			snapshot := readSnapshot(session)
			liveID, liveFile := ok(session.CurrentSessionID()).(string), ok(session.CurrentSessionFile()).(string)
			out["snapshotId"] = snapshot.id != "" && snapshot.id == liveID
			out["snapshotFile"] = strings.HasPrefix(snapshot.file, dir) && strings.HasSuffix(snapshot.file, ".jsonl") && strings.Contains(snapshot.file, snapshot.id) && snapshot.file == liveFile
			out["snapshotModel"] = snapshot.model["id"]
			out["snapshotThinking"] = snapshot.thinking
			out["snapshotTools"] = snapshot.tools
			out["snapshotPromptListsTool"] = strings.Contains(snapshot.prompt, "sentinel-snippet")
			out["liveStart"] = map[string]any{
				"model": ok(session.CurrentModel()).(map[string]any)["id"], "thinking": ok(session.CurrentThinkingLevel()), "prompt": ok(session.CurrentSystemPrompt()) == snapshot.prompt,
				"tools": ok(session.GetActiveToolNames()),
			}
			// The session changes; the snapshot does not follow it, the methods do.
			ok(nil, session.SetModel(context.Background(), map[string]any{"provider": "sentinel-think", "id": "echo-think"}))
			ok(nil, session.SetThinkingLevel("high"))
			ok(nil, session.SetActiveToolsByName([]string{}))
			livePrompt := ok(session.CurrentSystemPrompt()).(string)
			after := readSnapshot(session)
			out["afterChange"] = map[string]any{
				"liveTools": ok(session.GetActiveToolNames()), "snapshotTools": after.tools,
				"liveModel": ok(session.CurrentModel()).(map[string]any)["id"], "liveThinking": ok(session.CurrentThinkingLevel()),
				"snapshotModel": after.model["id"], "snapshotThinking": after.thinking,
				"livePromptDropsTool": livePrompt != snapshot.prompt && !strings.Contains(livePrompt, "sentinel-snippet"),
				"snapshotPrompt":      after.prompt == snapshot.prompt,
			}
			if failure != nil {
				return nil, failure
			}
			encoded, err := json.Marshal(out)
			return string(encoded), err
		},
	})
}
