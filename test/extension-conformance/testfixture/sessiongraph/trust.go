package sessiongraph

import (
	"fmt"
	"path/filepath"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func decision(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

func entry(value *sdk.ProjectTrustStoreEntry) any {
	if value == nil {
		return nil
	}
	return map[string]any{"path": value.Path, "decision": value.Decision}
}

func trustScript(ctx sdk.Context, args map[string]any) map[string]any {
	root, cwd, agentDir := fmt.Sprint(args["root"]), fmt.Sprint(args["cwd"]), fmt.Sprint(args["agentDir"])
	other := fmt.Sprint(args["otherDir"])
	sub, anc, ancProject, bare := filepath.Join(cwd, "sub"), filepath.Join(root, "anc"), filepath.Join(root, "anc", "proj"), filepath.Join(root, "bare")
	r := map[string]any{}
	store := ok(sdk.NewProjectTrustStore(ctx, agentDir))
	r["initial"] = map[string]any{"get": decision(ok(store.Get(cwd))), "entry": entry(ok(store.GetEntry(cwd)))}
	check(store.Set(cwd, new(true)))
	check(store.Set(other, new(false)))
	r["saved"] = map[string]any{
		"get": decision(ok(store.Get(cwd))), "entry": entry(ok(store.GetEntry(sub))), "otherGet": decision(ok(store.Get(other))), "file": readFile(filepath.Join(agentDir, "trust.json")),
	}
	check(store.SetMany([]sdk.ProjectTrustUpdate{{Path: cwd, Decision: nil}, {Path: anc, Decision: new(true)}}))
	r["many"] = map[string]any{
		"cwd": decision(ok(store.Get(cwd))), "anc": decision(ok(store.Get(ancProject))), "entryAnc": entry(ok(store.GetEntry(ancProject))), "file": readFile(filepath.Join(agentDir, "trust.json")),
	}
	check(store.Set(sub, new(false)))
	r["nested"] = map[string]any{"sub": decision(ok(store.Get(sub))), "parent": decision(ok(store.Get(cwd))), "file": readFile(filepath.Join(agentDir, "trust.json"))}

	failureOfGet := func(dir, contains string) string {
		broken, err := sdk.NewProjectTrustStore(ctx, filepath.Join(root, dir))
		check(err)
		_, err = broken.Get(cwd)
		return failure(err, contains)
	}
	r["errors"] = map[string]any{
		"notObject": failureOfGet("agent-array", "Invalid trust store"), "unreadable": failureOfGet("agent-bad-json", "Failed to read trust store"), "badValue": failureOfGet("agent-bad-value", "must be true, false, or null"),
	}
	r["resources"] = map[string]any{
		"config": ok(sdk.HasTrustRequiringProjectResources(ctx, cwd)), "bare": ok(sdk.HasTrustRequiringProjectResources(ctx, bare)), "ancestor": ok(sdk.HasTrustRequiringProjectResources(ctx, ancProject)),
	}
	return r
}
