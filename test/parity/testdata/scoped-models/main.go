package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// authoredScope reports the id-only model objects authored by this Pi probe. Native Go models are typed pointers; zero-valued unrelated struct fields are not additional probe inputs.
type authoredScope []extension.ScopedModel

func (models authoredScope) MarshalJSON() ([]byte, error) {
	values := make([]map[string]any, len(models))
	for i, scoped := range models {
		values[i] = map[string]any{"model": map[string]any{"id": scoped.Model.ID}}
		if scoped.ThinkingLevel != "" {
			values[i]["thinkingLevel"] = string(scoped.ThinkingLevel)
		}
	}
	return json.Marshal(values)
}

func main() {
	r := inproc.NewRunner(nil, ".")
	printJSON := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		fmt.Println(string(data))
	}
	read := func(ctx *extension.Context) authoredScope {
		value, err := ctx.ScopedModels()
		if err != nil {
			panic(err)
		}
		return authoredScope(value)
	}
	cold := extension.FromContext(r.DispatchContext(context.Background()))
	printJSON([]any{"default", read(cold)})
	scoped := []extension.ScopedModel{{Model: &ai.Model{ID: "scoped-test"}, ThinkingLevel: "high"}}
	r.BindCore(extension.ExtensionActions{}, extension.ContextActions{GetScopedModels: func() []extension.ScopedModel { return scoped }}, nil)
	event := extension.FromContext(r.DispatchContext(context.Background()))
	command := r.CreateCommandContext().Context
	same := func(ctx *extension.Context) bool {
		value := read(ctx)
		return len(value) == len(scoped) && len(value) > 0 && &value[0] == &scoped[0]
	}
	printJSON([]any{"bound", read(event), same(event), same(command), read(cold)})
	scoped = []extension.ScopedModel{{Model: &ai.Model{ID: "second"}}}
	printJSON([]any{"replaced", read(event), same(event), same(command)})
	r.BindCore(extension.ExtensionActions{}, extension.ContextActions{GetScopedModels: func() []extension.ScopedModel { return []extension.ScopedModel{} }}, nil)
	printJSON([]any{"captured", read(event), read(extension.FromContext(r.DispatchContext(context.Background())))})
	r.Invalidate("replaced")
	for _, ctx := range []*extension.Context{cold, event, command} {
		_, err := ctx.ScopedModels()
		printJSON(err.Error())
	}
}
