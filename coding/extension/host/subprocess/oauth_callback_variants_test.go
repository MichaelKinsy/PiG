package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// oauthCallbackExpectedRed lists known violations open on this branch, keyed
// by oauth.cb.* method; an entry whose violation is gone fails. It is empty
// since #201.
var oauthCallbackExpectedRed = map[string]string{}

// oauthCallbackMethods derives the oauth.cb.* wire methods from the protocol's constants.
func oauthCallbackMethods(t *testing.T) []string {
	t.Helper()
	path := filepath.Join(testenv.ModuleRoot(t), "coding", "extension", "host", "subprocess", "protocol.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	ast.Inspect(file, func(node ast.Node) bool {
		if lit, ok := node.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if value, err := strconv.Unquote(lit.Value); err == nil && strings.HasPrefix(value, "oauth.cb.") {
				methods = append(methods, value)
			}
		}
		return true
	})
	if len(methods) == 0 {
		t.Fatal("protocol.go declares no oauth.cb.* methods; update this check")
	}
	slices.Sort(methods)
	return methods
}

// probeCallbacks fills every callback of ai.OAuthLoginCallbacks: a callback
// that answers a prompt fails with failure, and every callback records its
// name when it runs.
func probeCallbacks(failure error, ran *[]string) ai.OAuthLoginCallbacks {
	var callbacks ai.OAuthLoginCallbacks
	value := reflect.ValueOf(&callbacks).Elem()
	for i := range value.NumField() {
		field := value.Type().Field(i)
		if field.Type.Kind() != reflect.Func {
			continue
		}
		name := field.Name
		value.Field(i).Set(reflect.MakeFunc(field.Type, func(args []reflect.Value) []reflect.Value {
			*ran = append(*ran, name)
			results := make([]reflect.Value, field.Type.NumOut())
			for j := range results {
				out := field.Type.Out(j)
				if out == reflect.TypeFor[error]() {
					results[j] = reflect.ValueOf(&failure).Elem()
				} else {
					results[j] = reflect.Zero(out)
				}
			}
			return results
		}))
	}
	return callbacks
}

// Every oauth.cb.* callback an extension's login flow sends reaches the login
// session, and a prompt whose handler fails reports that failure to the flow
// as an error. Pi runs the flow in process: a throwing onPrompt/onSelect
// rejects the flow's await with that error (interactive-mode.ts showAuthPrompt),
// and only the user's own cancel reads "Login cancelled".
func TestOAuthCallbackFailuresAreNotCancellation(t *testing.T) {
	failure := errors.New("probe: prompt handler failed")
	found := map[string]string{}
	for _, method := range oauthCallbackMethods(t) {
		var ran []string
		h := &Host{}
		h.setOAuthLoginSession("ext", probeCallbacks(failure, &ran))
		result, err := h.handleOAuthCallback(context.Background(), "ext", &CallPayload{Method: method, Args: json.RawMessage(`{"options":[{"id":"a","label":"A"}]}`)})
		prompted := slices.ContainsFunc(ran, answersPrompt)
		reported := err != nil && strings.Contains(err.Error(), failure.Error()) || result != nil && result.Error != nil && strings.Contains(result.Error.Message, failure.Error())
		switch {
		case len(ran) == 0:
			found[method] = "reached no login callback"
		case !prompted && err != nil:
			found[method] = "failed a notification: " + err.Error()
		case prompted && !reported && result != nil && strings.Contains(string(result.Result), `"cancel":true`):
			found[method] = "answered a handler failure with {cancel:true}"
		case prompted && !reported:
			found[method] = "dropped a prompt handler failure"
		}
	}
	for method, problem := range found {
		if reason := oauthCallbackExpectedRed[method]; reason != "" {
			t.Logf("expected red (%s): %s %s", reason, method, problem)
			continue
		}
		t.Errorf("%s %s", method, problem)
	}
	for method, reason := range oauthCallbackExpectedRed {
		if found[method] == "" {
			t.Errorf("oauthCallbackExpectedRed[%q] (%s) no longer fails: delete the entry", method, reason)
		}
	}
}

// answersPrompt reports whether the named login callback returns an answer, which can fail.
func answersPrompt(name string) bool {
	field, ok := reflect.TypeFor[ai.OAuthLoginCallbacks]().FieldByName(name)
	return ok && field.Type.NumOut() > 0 && field.Type.Out(field.Type.NumOut()-1) == reflect.TypeFor[error]()
}

// oauthVariantRoutedFindings lists login callback variants no oauth.cb.* wire
// method reaches. They are reported to the lead in handoffs/wiring-checks.md;
// an entry fails once its variant is reachable.
var oauthVariantRoutedFindings = map[string]string{}

// loginVariant names the variant a login callback field belongs to: a Context
// form and its plain form are one variant.
func loginVariant(field string) string {
	return strings.TrimSuffix(field, "Context")
}

// loginFallbacks maps a login callback variant that runs only when another is
// nil to that variant (ai/oauth_types.go: OnManualCodeInput is the message-less
// fallback of OnManualCodePrompt). The probe sets every callback, so a
// fallback is not a variant of its own; its primary must be reached, and
// reaching only the fallback leaves the primary unreached.
var loginFallbacks = map[string]string{"OnManualCodeInput": "OnManualCodePrompt"}

// Every prompt and event variant of the login callbacks is reachable from an
// extension through some oauth.cb.* method (variants as loginVariant names
// them, fallbacks excluded).
func TestOAuthLoginVariantsAreReachableFromTheWire(t *testing.T) {
	reached := map[string]bool{}
	for _, method := range oauthCallbackMethods(t) {
		var ran []string
		h := &Host{}
		h.setOAuthLoginSession("ext", probeCallbacks(errors.New("probe"), &ran))
		_, _ = h.handleOAuthCallback(context.Background(), "ext", &CallPayload{Method: method, Args: json.RawMessage(`{"options":[{"id":"a","label":"A"}]}`)})
		for _, name := range ran {
			reached[loginVariant(name)] = true
		}
	}
	callbacks := reflect.TypeFor[ai.OAuthLoginCallbacks]()
	variants := map[string]bool{}
	for i := range callbacks.NumField() {
		field := callbacks.Field(i)
		// A variant either answers (its last result is an error) or only notifies (no result).
		if field.Type.Kind() == reflect.Func && (field.Type.NumOut() == 0 || answersPrompt(field.Name)) && loginFallbacks[loginVariant(field.Name)] == "" {
			variants[loginVariant(field.Name)] = true
		}
	}
	for fallback, primary := range loginFallbacks {
		if !variants[primary] {
			t.Errorf("loginFallbacks[%q] names %q, which is not a login callback variant", fallback, primary)
		}
	}
	for _, variant := range slices.Sorted(maps.Keys(variants)) {
		switch {
		case reached[variant] && oauthVariantRoutedFindings[variant] != "":
			t.Errorf("oauthVariantRoutedFindings[%q] is reachable now: delete the entry", variant)
		case reached[variant]:
		case oauthVariantRoutedFindings[variant] != "":
			t.Logf("routed finding (%s): no oauth.cb method reaches %s", oauthVariantRoutedFindings[variant], variant)
		default:
			t.Errorf("no oauth.cb method reaches the %s login callback: an extension flow's variant is dropped", variant)
		}
	}
}
