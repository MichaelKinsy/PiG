package testfixture

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// replicatedAccessorsProbe reads every replicated accessor, writes through the matching API and reads it again at once, the order Pi's synchronous
// Session guarantees (agent-session.ts setSessionName, sessionManager.appendLabelChange, sendCustomMessage, setModel). Each SDK fixture reports
// the same JSON object (TestReplicatedAccessorsReadYourWritesAcrossSDKs).
func replicatedAccessorsProbe(ctx sdk.Context, _ string) error {
	out := map[string]any{"name0": ctx.SessionName(), "model0": ctx.Model(), "provider0": ctx.ModelProvider()}
	lastType := func() (string, error) {
		entries, err := ctx.GetEntries()
		if err != nil || len(entries) == 0 {
			return "", err
		}
		var last struct {
			Type string `json:"type"`
		}
		err = json.Unmarshal(entries[len(entries)-1], &last)
		return last.Type, err
	}
	if err := ctx.SetSessionName("renamed"); err != nil {
		return err
	}
	out["name1"] = ctx.SessionName()
	if err := ctx.SetLabel("one", "lab"); err != nil {
		return err
	}
	afterLabel, err := lastType()
	if err != nil {
		return err
	}
	out["entryAfterLabel"] = afterLabel
	if err := ctx.SendMessage("probe", "hi", true, sdk.SendMessageOptions{}); err != nil {
		return err
	}
	afterMessage, err := lastType()
	if err != nil {
		return err
	}
	out["entryAfterMessage"] = afterMessage
	if _, err := ctx.SetModel("prov-after/id-after"); err != nil {
		return err
	}
	out["model1"], out["provider1"], out["qualified1"] = ctx.Model(), ctx.ModelProvider(), ctx.ModelQualified()
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	ctx.Notify(string(data), "info")
	return nil
}
