package codingagent

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// nativeFieldExpectedRed lists known field drops open on this branch, keyed by
// field path; an entry whose field survives fails. It is empty since #201.
var nativeFieldExpectedRed = map[string]string{}

// probeFunc returns a function of typ that answers with an argument of its
// result type when it has one (a filter returns its input), the probe's models
// for a model list, a configured auth check, and zero values otherwise.
func probeFunc(typ reflect.Type, chat []*ai.Model, all []ai.AnyModel, called func()) reflect.Value {
	return reflect.MakeFunc(typ, func(args []reflect.Value) []reflect.Value {
		if called != nil {
			called()
		}
		results := make([]reflect.Value, typ.NumOut())
		for i := range results {
			out := typ.Out(i)
			results[i] = reflect.Zero(out)
			for _, arg := range args {
				if arg.Type() == out && out.Kind() == reflect.Slice {
					results[i] = arg
				}
			}
			switch out {
			case reflect.TypeFor[[]*ai.Model]():
				results[i] = reflect.ValueOf(chat)
			case reflect.TypeFor[[]ai.AnyModel]():
				results[i] = reflect.ValueOf(all)
			case reflect.TypeFor[*ai.AuthCheck]():
				results[i] = reflect.ValueOf(&ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "probe"})
			case reflect.TypeFor[*ai.AuthResult]():
				results[i] = reflect.ValueOf(&ai.AuthResult{Auth: ai.ModelAuth{APIKey: "probe"}})
			case reflect.TypeFor[*ai.AssistantMessageEventStream]():
				results[i] = reflect.ValueOf(ai.NewAssistantMessageEventStream())
			case reflect.TypeFor[bool]():
				results[i] = reflect.ValueOf(true)
			}
		}
		return results
	})
}

// fillAuth sets every leaf of an auth value: strings to "probe-<path>", bools
// to true, pointers to a filled value and functions to probes that record
// their path in called when they run.
func fillAuth(value reflect.Value, path string, called map[string]bool) {
	switch value.Kind() {
	case reflect.Pointer:
		value.Set(reflect.New(value.Type().Elem()))
		fillAuth(value.Elem(), path, called)
	case reflect.Struct:
		for i := range value.NumField() {
			if value.Type().Field(i).IsExported() {
				fillAuth(value.Field(i), path+"."+value.Type().Field(i).Name, called)
			}
		}
	case reflect.String:
		value.SetString("probe-" + path)
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Func:
		value.Set(probeFunc(value.Type(), nil, nil, func() { called[path] = true }))
	}
}

// identifyFunc calls fn and reports which probe answered: "probe <path>" when
// the declared function ran, "foreign" when another one did.
func identifyFunc(fn reflect.Value, called map[string]bool) (identity string) {
	clear(called)
	defer func() {
		if recover() != nil {
			identity = "foreign"
		}
		for path := range called {
			identity = "probe " + path
		}
	}()
	args := make([]reflect.Value, fn.Type().NumIn())
	for i := range args {
		if fn.Type().In(i) == reflect.TypeFor[context.Context]() {
			args[i] = reflect.ValueOf(context.Background())
		} else {
			args[i] = reflect.Zero(fn.Type().In(i))
		}
	}
	fn.Call(args)
	return "foreign"
}

// authLeaves lists every exported leaf of an auth value by dotted path. A
// function leaf is the probe that answers it, or "nil"; a pointer contributes
// its own presence.
func authLeaves(value reflect.Value, path string, out map[string]string, called map[string]bool) {
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			out[path] = "nil"
			return
		}
		out[path] = "set"
		authLeaves(value.Elem(), path, out, called)
	case reflect.Struct:
		for i := range value.NumField() {
			if value.Type().Field(i).IsExported() {
				authLeaves(value.Field(i), path+"."+value.Type().Field(i).Name, out, called)
			}
		}
	case reflect.Func:
		if value.IsNil() {
			out[path] = "nil"
		} else {
			out[path] = identifyFunc(value, called)
		}
	default:
		out[path] = fmt.Sprint(value.Interface())
	}
}

// A provider object's registration keeps every field an extension declared
// through every path that writes or refreshes it: registration, the queued
// local refresh, a network refresh and a re-registration. The registry is read
// back through the auth /login composes (registryAuthConfig) and its model
// catalog, so a field dropped by any republication fails by name.
func TestNativeProviderRegistrationKeepsEveryField(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	registry.SetCredentialStore(ai.NewInMemoryAuthStorage(map[string]ai.Credential{}))
	models := []extension.ProviderModelConfig{{ID: "probe-model", Name: "Probe Model", API: ai.APIOpenAICompletions, BaseURL: "https://probe.invalid"}}
	all, err := nativeTypedModels("probe-native", models)
	if err != nil {
		t.Fatal(err)
	}
	var chat []*ai.Model
	for _, model := range all {
		if typed, ok := model.(*ai.Model); ok {
			chat = append(chat, typed)
		}
	}
	carrier := &extension.NativeProvider{}
	value := reflect.ValueOf(carrier).Elem()
	for i := range value.NumField() {
		field := value.Type().Field(i)
		if field.Type.Kind() == reflect.Func {
			value.Field(i).Set(probeFunc(field.Type, chat, all, nil))
		}
	}
	carrier.ID, carrier.Name, carrier.BaseURL, carrier.Models = "probe-native", "Probe Native", "https://probe.invalid", models
	carrier.Headers = ai.ProviderHeadersFromStrings(map[string]string{"X-Probe": "1"})
	called := map[string]bool{}
	fillAuth(reflect.ValueOf(&carrier.Auth).Elem(), "Auth", called)
	want := map[string]string{}
	authLeaves(reflect.ValueOf(carrier.Auth), "Auth", want, called)

	found := map[string]string{}
	check := func(stage string) {
		got := map[string]string{}
		auth, _, ok := registry.registryAuthConfig(carrier.ID)
		if !ok {
			found["registered"] = stage + ": the registry does not know the provider"
			return
		}
		authLeaves(reflect.ValueOf(auth), "Auth", got, called)
		for path, value := range want {
			if got[path] != value && found[path] == "" {
				found[path] = fmt.Sprintf("%s: %s is %q, declared %q", stage, path, got[path], value)
			}
		}
		if !registry.HasModelDefinition(carrier.ID, "probe-model") && found["Models"] == "" {
			found["Models"] = stage + ": the declared model is gone"
		}
		if registry.NativeProvider(carrier.ID) != carrier && found["object"] == "" {
			found["object"] = stage + ": the registry no longer holds the provider object"
		}
	}
	ctx := context.Background()
	if err := registry.RegisterNativeProvider(ctx, carrier); err != nil {
		t.Fatal(err)
	}
	check("registration")
	registry.YieldToRegistrationRefresh(ctx)
	check("local refresh")
	force := true
	for id, err := range registry.refreshNativeProviders(ctx, nil, true, &force) {
		t.Fatalf("network refresh of %s: %v", id, err)
	}
	check("network refresh")
	if err := registry.RegisterNativeProvider(ctx, carrier); err != nil {
		t.Fatal(err)
	}
	registry.YieldToRegistrationRefresh(ctx)
	check("re-registration")

	for _, path := range slices.Sorted(maps.Keys(found)) {
		if reason := nativeFieldExpectedRed[path]; reason != "" {
			t.Logf("expected red (%s): %s", reason, found[path])
			continue
		}
		t.Errorf("%s", found[path])
	}
	for path, reason := range nativeFieldExpectedRed {
		if found[path] == "" {
			t.Errorf("nativeFieldExpectedRed[%q] (%s) survives now: delete the entry", path, reason)
		}
	}
}

// providerConfigRoutedFindings lists ProviderConfig fields the registry drops
// outside #201, reported to the lead in handoffs/wiring-checks.md; an entry
// fails once the field survives.
var providerConfigRoutedFindings = map[string]string{}

// providerConfigConsumed are ProviderConfig fields the registry consumes into
// another form instead of keeping them under their own name.
var providerConfigConsumed = map[string]string{
	"OAuth.UsesCallbackServer": "retained for source compatibility only; Pi's auth flows ignore it (types.ts:1937-1938)",
}

// fillAll sets every exported leaf: strings to "probe-<path>", numbers to 7,
// bools to true, pointers, maps and slices to one filled element, and
// functions to probes.
func fillAll(value reflect.Value, path string) {
	switch value.Kind() {
	case reflect.Pointer:
		value.Set(reflect.New(value.Type().Elem()))
		fillAll(value.Elem(), path)
	case reflect.Struct:
		for i := range value.NumField() {
			if value.Type().Field(i).IsExported() {
				fillAll(value.Field(i), path+"."+value.Type().Field(i).Name)
			}
		}
	case reflect.String:
		value.SetString("probe-" + path)
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Int, reflect.Int64, reflect.Int32:
		value.SetInt(7)
	case reflect.Float64, reflect.Float32:
		value.SetFloat(7)
	case reflect.Map:
		value.Set(reflect.MakeMap(value.Type()))
		key, elem := reflect.New(value.Type().Key()).Elem(), reflect.New(value.Type().Elem()).Elem()
		fillAll(key, path+".key")
		fillAll(elem, path)
		value.SetMapIndex(key, elem)
	case reflect.Slice:
		elem := reflect.New(value.Type().Elem()).Elem()
		fillAll(elem, path)
		value.Set(reflect.Append(reflect.MakeSlice(value.Type(), 0, 1), elem))
	case reflect.Func:
		value.Set(probeFunc(value.Type(), nil, nil, nil))
	}
}

// deref unwraps pointers and interfaces.
func deref(value reflect.Value) reflect.Value {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) && !value.IsNil() {
		value = value.Elem()
	}
	return value
}

// fieldByFold finds a struct field by case-insensitive name.
func fieldByFold(value reflect.Value, name string) (reflect.Value, bool) {
	value = deref(value)
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	field, ok := value.Type().FieldByNameFunc(func(candidate string) bool { return strings.EqualFold(candidate, name) })
	if !ok {
		return reflect.Value{}, false
	}
	return value.FieldByIndex(field.Index), true
}

// compareFields reports every exported leaf of want that got lacks or holds
// differently, matching fields by case-insensitive name at every depth. A
// function, map or pointer must be set when it was registered set; a list must
// keep its length and each element's ID; a scalar must keep its value.
func compareFields(want, got reflect.Value, path string, found map[string]string, stage string, skip ...string) {
	want = deref(want)
	for i := range want.NumField() {
		field := want.Type().Field(i)
		name := strings.TrimPrefix(path+"."+field.Name, ".")
		if !field.IsExported() || found[name] != "" || slices.Contains(skip, name) {
			continue
		}
		registered := want.Field(i)
		have, ok := fieldByFold(got, field.Name)
		if !ok {
			found[name] = stage + ": the registry keeps no " + name
			continue
		}
		switch registered.Kind() {
		case reflect.Func, reflect.Map, reflect.Pointer, reflect.Interface, reflect.Slice:
			if registered.IsNil() {
				continue
			}
			if (have.Kind() == reflect.Func || have.Kind() == reflect.Map || have.Kind() == reflect.Pointer || have.Kind() == reflect.Interface || have.Kind() == reflect.Slice) && have.IsNil() {
				found[name] = stage + ": " + name + " was dropped"
				continue
			}
		}
		switch inner := deref(registered); {
		case registered.Kind() == reflect.Func:
		case registered.Kind() == reflect.Map:
			if deref(have).Len() != registered.Len() {
				found[name] = fmt.Sprintf("%s: %s has %d entries, registered %d", stage, name, deref(have).Len(), registered.Len())
			}
		case registered.Kind() == reflect.Slice:
			if deref(have).Len() != registered.Len() {
				found[name] = fmt.Sprintf("%s: %s has %d elements, registered %d", stage, name, deref(have).Len(), registered.Len())
				continue
			}
			for j := range registered.Len() {
				wantID, _ := fieldByFold(registered.Index(j), "ID")
				haveID, _ := fieldByFold(deref(have).Index(j), "ID")
				if wantID.IsValid() && (!haveID.IsValid() || fmt.Sprint(haveID.Interface()) != fmt.Sprint(wantID.Interface())) {
					found[name] = fmt.Sprintf("%s: %s element %d lost its ID", stage, name, j)
				}
			}
		case inner.Kind() == reflect.Struct:
			compareFields(inner, have, name, found, stage)
		default:
			if fmt.Sprint(deref(have).Interface()) != fmt.Sprint(inner.Interface()) {
				found[name] = fmt.Sprintf("%s: %s is %v, registered %v", stage, name, deref(have).Interface(), inner.Interface())
			}
		}
	}
}

// An extension's registerProvider configuration keeps every field through
// registration, a refresh and a re-registration (Pi model-runtime.ts
// registerProvider stores the configuration it is given).
func TestProviderConfigRegistrationKeepsEveryField(t *testing.T) {
	found := map[string]string{}
	// A registration with refreshModels is composed in the native collection (provider-composer.ts:613); the others stay configuration.
	for _, path := range []string{"refreshModels", "configuration"} {
		registry := NewModelRegistry(t.TempDir())
		registry.SetCredentialStore(ai.NewInMemoryAuthStorage(map[string]ai.Credential{}))
		var config extension.ProviderConfig
		fillAll(reflect.ValueOf(&config).Elem(), "config")
		// A registration must name a real API for itself and its model; every other leaf keeps its probe value.
		config.API = ai.APIOpenAICompletions
		config.Models = []extension.ProviderModelConfig{{ID: "probe-model", Name: "Probe Model", API: ai.APIOpenAICompletions}}
		if path == "configuration" {
			config.RefreshModels = nil
		}
		stageFound := map[string]string{}
		check := func(stage string) {
			registry.mu.RLock()
			got, ok := registry.dynamic["probe-config"]
			registry.mu.RUnlock()
			if input := registry.GetRegisteredProviderConfig("probe-config"); input != nil {
				compareFields(reflect.ValueOf(config), reflect.ValueOf(*input), "", stageFound, stage)
				return
			}
			if !ok {
				t.Fatalf("%s %s: the registry lost the provider", path, stage)
			}
			// A configuration keeps its OAuth discovery metadata in OAuth and the login callbacks beside it.
			compareFields(reflect.ValueOf(config), reflect.ValueOf(got), "", stageFound, stage, "OAuth")
			if got.oauthCallbacks == nil {
				stageFound["OAuth"] = stage + ": OAuth was dropped"
				return
			}
			compareFields(reflect.ValueOf(config.OAuth), reflect.ValueOf(got.oauthCallbacks), "OAuth", stageFound, stage)
		}
		if err := registry.RegisterExtensionProvider("probe-config", config); err != nil {
			t.Fatal(err)
		}
		check("registration")
		registry.Refresh()
		check("refresh")
		if err := registry.RegisterExtensionProvider("probe-config", config); err != nil {
			t.Fatal(err)
		}
		check("re-registration")
		for name, problem := range stageFound {
			if providerConfigConsumed[name] != "" {
				found[name] = problem
			} else {
				found[path+" "+name] = problem
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(found)) {
		switch {
		case providerConfigRoutedFindings[name] != "":
			t.Logf("routed finding (%s): %s", providerConfigRoutedFindings[name], found[name])
		case providerConfigConsumed[name] != "":
			t.Logf("consumed (%s): %s", providerConfigConsumed[name], found[name])
		default:
			t.Errorf("%s", found[name])
		}
	}
	for _, list := range []map[string]string{providerConfigRoutedFindings, providerConfigConsumed} {
		for name, reason := range list {
			if found[name] == "" {
				t.Errorf("%s (%s) survives now: delete the entry", name, reason)
			}
		}
	}
}
