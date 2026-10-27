package sessiongraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// createProviderScript is pi-ai's createProvider over the parts of testdata/session-graph.mjs createProviderScript; the report is Pi's own run's.
func createProviderScript() (report map[string]any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%v", recovered)
		}
	}()
	r := map[string]any{}
	auth := sdk.ProviderAuth{APIKey: &sdk.APIKeyAuth{Name: "Key", Resolve: func(sdk.APIKeyAuthInput) (*sdk.AuthResult, error) {
		return &sdk.AuthResult{Auth: map[string]any{"apiKey": "k"}}, nil
	}}}
	M := func(id, api string, extra ...map[string]any) map[string]any {
		model := map[string]any{"id": id, "name": id, "api": api, "provider": "cp"}
		for _, fields := range extra {
			maps.Copy(model, fields)
		}
		return model
	}
	message := func(model map[string]any, text string) map[string]any {
		return map[string]any{
			"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}, "api": model["api"], "provider": model["provider"], "model": model["id"],
			"usage":      map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
			"stopReason": "stop", "timestamp": 1,
		}
	}
	scripted := func(tag string) sdk.ProviderStreamFunc {
		return func(model, _ map[string]any, _ sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
			stream := sdk.CreateAssistantMessageEventStream()
			stream.Push(map[string]any{"type": "done", "reason": "stop", "message": message(model, tag+":"+model["id"].(string))})
			return stream, nil
		}
	}
	impl := func(tag string) sdk.ProviderStreams {
		return sdk.ProviderStreams{Stream: scripted(tag + ".stream"), StreamSimple: scripted(tag + ".simple")}
	}
	drain := func(stream *sdk.ModelEventStream, streamErr error) map[string]any {
		if streamErr != nil {
			panic(streamErr)
		}
		types := []string{}
		for event := range stream.Events(context.Background()) {
			types = append(types, event["type"].(string))
		}
		result := stream.Result()
		var text strings.Builder
		for _, block := range result["content"].([]any) {
			if value, ok := block.(map[string]any)["text"].(string); ok {
				text.WriteString(value)
			}
		}
		usage := result["usage"].(map[string]any)
		return map[string]any{
			"types": types, "text": text.String(), "stopReason": result["stopReason"], "error": result["errorMessage"], "api": result["api"], "provider": result["provider"], "model": result["model"],
			"usageZero": fmt.Sprint(usage["totalTokens"]) == "0", "timestampIsNumber": isNumber(result["timestamp"]),
		}
	}
	thrown := func(fn func() error) string {
		if err := fn(); err != nil {
			return err.Error()
		}
		return "no error"
	}
	base := []map[string]any{M("c1", "api-a"), M("c2", "api-b"), M("i1", "img-a", map[string]any{"type": "image"}), M("k1", "cls-a", map[string]any{"type": "classifier"})}
	baseURL := "http://x"
	p := ok(sdk.CreateProvider(sdk.CreateProviderOptions{ID: "cp", Auth: auth, Models: base, APIs: map[string]sdk.ProviderStreams{"api-a": impl("a"), "api-b": impl("b")}, BaseURL: &baseURL, Headers: map[string]string{"h": "1"}}))
	single := impl("s")
	named := ok(sdk.CreateProvider(sdk.CreateProviderOptions{ID: "cp2", Name: "Named", Auth: auth, API: &single}))
	methods := func(provider *sdk.Provider) map[string]any {
		kind := func(present bool) string {
			if present {
				return "function"
			}
			return "undefined"
		}
		return map[string]any{
			"fetchDeferred": kind(provider.FetchDeferred != nil), "cancelDeferred": kind(provider.CancelDeferred != nil), "generateImages": kind(provider.GenerateImages != nil), "classify": kind(provider.Classify != nil),
			"refreshModels": kind(provider.RefreshModels != nil), "filterModels": kind(provider.FilterModels != nil), "filterAllModels": kind(provider.FilterAllModels != nil),
		}
	}
	r["meta"] = map[string]any{"id": p.ID, "name": p.Name, "baseUrl": *p.BaseURL, "headers": p.Headers, "named": named.Name, "methods": methods(p)}
	ids := func(models []map[string]any) []string {
		out := []string{}
		for _, model := range models {
			out = append(out, model["id"].(string))
		}
		return out
	}
	r["models"] = map[string]any{"chat": ids(ok(p.GetModels())), "all": ids(ok(p.GetAllModels()))}
	opts := sdk.ProviderStreamOptions{}
	sp := ok(sdk.CreateProvider(sdk.CreateProviderOptions{ID: "sp", Auth: auth, API: &single}))
	r["dispatch"] = map[string]any{
		"a": drain(p.Stream(base[0], nil, opts)), "b": drain(p.StreamSimple(base[1], nil, opts)), "missing": drain(p.Stream(M("c9", "api-z"), nil, opts)),
		"noApi":  drain(p.StreamSimple(map[string]any{"id": "c8", "provider": "cp"}, nil, opts)),
		"single": drain(sp.Stream(M("x", "anything"), nil, opts)),
	}
	_, errNone := sdk.CreateProvider(sdk.CreateProviderOptions{ID: "e1", Auth: auth})
	_, errEmpty := sdk.CreateProvider(sdk.CreateProviderOptions{ID: "e2", Auth: auth, APIs: map[string]sdk.ProviderStreams{}, Images: map[string]sdk.ProviderImagesFunc{}, Classifiers: map[string]sdk.ProviderClassifyFunc{}})
	r["invalid"] = map[string]any{"none": thrown(func() error { return errNone }), "empty": thrown(func() error { return errEmpty })}
	cancelled := []string{}
	apiA := impl("a")
	apiA.FetchDeferred = func(model, handle map[string]any, o sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
		return scripted("a.fetch."+handle["id"].(string))(model, handle, o)
	}
	apiA.CancelDeferred = func(_, handle map[string]any, _ sdk.ProviderStreamOptions) error {
		cancelled = append(cancelled, handle["id"].(string))
		return nil
	}
	d := ok(sdk.CreateProvider(sdk.CreateProviderOptions{ID: "dp", Auth: auth, APIs: map[string]sdk.ProviderStreams{"api-a": apiA, "api-b": impl("b")}}))
	brokenA := impl("a")
	brokenA.FetchDeferred = func(_, _ map[string]any, _ sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
		return nil, errors.New("fetch boom")
	}
	broken := ok(sdk.CreateProvider(sdk.CreateProviderOptions{ID: "bp", Auth: auth, APIs: map[string]sdk.ProviderStreams{"api-a": brokenA}}))
	handle := func(id string) map[string]any { return map[string]any{"id": id} }
	r["deferred"] = map[string]any{
		"fetched":     drain(d.FetchDeferred(M("c1", "api-a"), handle("h1"), opts)),
		"unsupported": drain(d.FetchDeferred(M("c2", "api-b"), handle("h2"), opts)),
		"noApi":       drain(d.FetchDeferred(M("c3", "api-z"), handle("h3"), opts)),
		"thrown":      drain(broken.FetchDeferred(M("c1", "api-a"), handle("h4"), opts)),
		"cancelOk":    thrown(func() error { return d.CancelDeferred(M("c1", "api-a"), handle("h1"), opts) }), "cancelled": cancelled,
		"cancelUnsupported": thrown(func() error { return d.CancelDeferred(M("c2", "api-b"), handle("h2"), opts) }),
		"cancelNoApi":       thrown(func() error { return d.CancelDeferred(M("c3", "api-z"), handle("h3"), opts) }),
	}
	pick := func(result map[string]any) map[string]any {
		return map[string]any{
			"api": result["api"], "provider": result["provider"], "model": result["model"], "stopReason": result["stopReason"], "error": result["errorMessage"], "output": result["output"], "answers": result["answers"],
			"timestampIsNumber": isNumber(result["timestamp"]),
		}
	}
	o := ok(sdk.CreateProvider(sdk.CreateProviderOptions{
		ID: "op", Auth: auth,
		Images: map[string]sdk.ProviderImagesFunc{"img-a": func(model, _ map[string]any, _ sdk.ProviderOperationOptions) (map[string]any, error) {
			return map[string]any{"api": model["api"], "provider": model["provider"], "model": model["id"], "output": []any{map[string]any{"type": "image", "data": "x"}}, "stopReason": "stop", "timestamp": 1}, nil
		}},
		Classifiers: map[string]sdk.ProviderClassifyFunc{"cls-a": func(model map[string]any, _ sdk.ClassifierContext, _ sdk.ProviderOperationOptions) (sdk.ClassifierResult, error) {
			return sdk.ClassifierResult{API: model["api"].(string), Provider: model["provider"].(string), Model: model["id"].(string), Answers: sdk.NewOrderedObject("q", "a"), StopReason: "stop", Timestamp: 1}, nil
		}},
	}))
	classified := func(model map[string]any) map[string]any {
		result := ok(o.Classify(model, sdk.ClassifierContext{}, sdk.ProviderOperationOptions{}))
		answers := map[string]any{}
		if result.Answers != nil {
			encoded := ok(json.Marshal(result.Answers))
			ok0(json.Unmarshal(encoded, &answers))
		}
		var errorMessage any
		if result.ErrorMessage != "" {
			errorMessage = result.ErrorMessage
		}
		return map[string]any{"api": result.API, "provider": result.Provider, "model": result.Model, "stopReason": result.StopReason, "error": errorMessage, "output": nil, "answers": answers, "timestampIsNumber": result.Timestamp != 0}
	}
	generated := func(model map[string]any) map[string]any {
		out := pick(ok(o.GenerateImages(model, nil, sdk.ProviderOperationOptions{})))
		return out
	}
	r["operations"] = map[string]any{
		"methods": map[string]any{"stream": "function", "generateImages": "function", "classify": "function"},
		"image":   generated(M("i1", "img-a", map[string]any{"type": "image"})), "imageMissing": generated(M("i2", "img-z", map[string]any{"type": "image"})),
		"classify": classified(M("k1", "cls-a", map[string]any{"type": "classifier"})), "classifyMissing": classified(M("k2", "cls-z", map[string]any{"type": "classifier"})),
	}
	fetchCalls := []bool{}
	publications := []map[string]any{}
	f := ok(sdk.CreateProvider(sdk.CreateProviderOptions{
		ID: "fp", Auth: auth, Models: []map[string]any{M("c1", "api-a", map[string]any{"name": "Base One"}), M("c2", "api-a")}, API: &single,
		FetchModels: func(refresh sdk.RefreshModelsContext) ([]map[string]any, error) {
			fetchCalls = append(fetchCalls, refresh.AllowNetwork)
			return []map[string]any{M("c1", "api-a", map[string]any{"name": "Fetched One"}), M("c3", "api-a"), M("u1", "api-a", map[string]any{"type": "future"}), M("i1", "img-a", map[string]any{"type": "image"})}, nil
		},
	}))
	catalog := func() []string {
		out := []string{}
		for _, model := range ok(f.GetAllModels()) {
			kind := "chat"
			if value, has := model["type"].(string); has {
				kind = value
			}
			out = append(out, fmt.Sprintf("%v:%v:%s", model["id"], model["name"], kind))
		}
		return out
	}
	refreshContext := func(allowNetwork bool, signal context.Context, stored map[string]any, publish func(sdk.ModelsPublication) (bool, error)) sdk.RefreshModelsContext {
		if publish == nil {
			publish = func(publication sdk.ModelsPublication) (bool, error) {
				entry := map[string]any{"persist": "omitted", "update": "undefined"}
				if publication.Persist != nil {
					var persisted struct {
						Models    []map[string]any `json:"models"`
						CheckedAt any              `json:"checkedAt"`
					}
					ok0(json.Unmarshal(publication.Persist, &persisted))
					entry["persist"] = map[string]any{"models": ids(persisted.Models), "checkedAt": "number"}
				}
				if publication.Update != nil {
					entry["update"] = "function"
					ok0(publication.Update())
				}
				publications = append(publications, entry)
				return true, nil
			}
		}
		return sdk.RefreshModelsContext{AllowNetwork: allowNetwork, Signal: signal, Stored: stored, Publish: publish}
	}
	stored := map[string]any{"models": []any{M("c1", "api-a", map[string]any{"name": "Stored One", "provider": "fp"}), M("z9", "api-a", map[string]any{"provider": "other"})}}
	r["refresh"] = map[string]any{"present": "function", "absent": "undefined", "initial": catalog()}
	refresh := r["refresh"].(map[string]any)
	ok0(f.RefreshModels(refreshContext(false, context.Background(), stored, nil)))
	refresh["restoredOffline"] = map[string]any{"catalog": catalog(), "fetchCalls": slices.Clone(fetchCalls), "publications": takePublications(&publications)}
	aborted, cancel := context.WithCancel(context.Background())
	cancel()
	ok0(f.RefreshModels(refreshContext(true, aborted, stored, nil)))
	refresh["aborted"] = map[string]any{"fetchCalls": slices.Clone(fetchCalls), "publications": takePublications(&publications)}
	ok0(f.RefreshModels(refreshContext(true, context.Background(), stored, func(sdk.ModelsPublication) (bool, error) { return false, nil })))
	refresh["refused"] = map[string]any{"catalog": catalog(), "fetchCalls": slices.Clone(fetchCalls)}
	ok0(f.RefreshModels(refreshContext(true, context.Background(), nil, nil)))
	refresh["fetched"] = map[string]any{"catalog": catalog(), "fetchCalls": slices.Clone(fetchCalls), "publications": takePublications(&publications), "chat": ids(ok(f.GetModels()))}
	return r, nil
}

func takePublications(list *[]map[string]any) []map[string]any {
	out := *list
	*list = []map[string]any{}
	return out
}

func ok0(err error) {
	if err != nil {
		panic(err)
	}
}

func isNumber(value any) bool {
	switch value.(type) {
	case int, int64, float64:
		return true
	}
	return false
}
