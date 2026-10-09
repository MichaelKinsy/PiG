package codingagent

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// loginVariantExpectedRed lists known violations open on this branch, keyed by
// callback; an entry whose violation is gone fails. It is empty since #201.
var loginVariantExpectedRed = map[string]string{}

// loginVariantRoutedFindings lists violations outside #201, reported to the
// lead in handoffs/wiring-checks.md; an entry fails once fixed.
var loginVariantRoutedFindings = map[string]string{}

type variantProbeProvider struct {
	probe func(ai.OAuthLoginCallbacks)
	done  chan struct{}
}

func (p *variantProbeProvider) ID() string                           { return "variant-probe" }
func (p *variantProbeProvider) Name() string                         { return "Variant Probe" }
func (p *variantProbeProvider) UsesCallbackServer() bool             { return false }
func (p *variantProbeProvider) GetAPIKey(ai.OAuthCredentials) string { return "" }
func (p *variantProbeProvider) RefreshToken(ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, errors.New("unused")
}
func (p *variantProbeProvider) Login(callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	defer close(p.done)
	p.probe(callbacks)
	return ai.OAuthCredentials{}, errors.New("probe complete")
}

// fillProbe returns a value of typ with every string set and every slice
// holding one such element, so a select prompt has an option to show.
func fillProbe(typ reflect.Type) reflect.Value {
	value := reflect.New(typ).Elem()
	switch typ.Kind() {
	case reflect.String:
		value.SetString("probe")
	case reflect.Struct:
		for i := range typ.NumField() {
			if typ.Field(i).IsExported() {
				value.Field(i).Set(fillProbe(typ.Field(i).Type))
			}
		}
	case reflect.Slice:
		value.Set(reflect.Append(reflect.MakeSlice(typ, 0, 1), fillProbe(typ.Elem())))
	}
	return value
}

// Every prompt and event variant of the login callbacks is handled by the
// interactive login of a registered OAuth provider, the path an extension's
// login takes. Pi's showAuthPrompt answers a prompt whose signal is already
// aborted with "Login cancelled" for every prompt type (interactive-mode.ts
// showAuthPrompt), so a prompt callback given a cancelled context must reject
// with exactly that; any other answer means the variant never reaches the
// dialog. An event callback must exist: a nil one drops the event.
func TestRegisteredOAuthLoginHandlesEveryCallbackVariant(t *testing.T) {
	found := map[string]string{}
	callbacksType := reflect.TypeFor[ai.OAuthLoginCallbacks]()
	contextType := reflect.TypeFor[context.Context]()
	provider := &variantProbeProvider{done: make(chan struct{}), probe: func(callbacks ai.OAuthLoginCallbacks) {
		value := reflect.ValueOf(callbacks)
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		for i := range callbacksType.NumField() {
			field := callbacksType.Field(i)
			fn := value.Field(i)
			if field.Type.Kind() != reflect.Func {
				continue
			}
			answers := field.Type.NumOut() == 2 && field.Type.Out(1) == reflect.TypeFor[error]()
			event := field.Type.NumOut() == 0
			switch {
			case event && fn.IsNil():
				found[field.Name] = "is nil, so the event is dropped"
			case answers && field.Type.NumIn() > 0 && field.Type.In(0) == contextType && fn.IsNil():
				found[field.Name] = "is nil, so the prompt never opens"
			case answers && field.Type.NumIn() > 0 && field.Type.In(0) == contextType:
				args := []reflect.Value{reflect.ValueOf(cancelled)}
				for j := 1; j < field.Type.NumIn(); j++ {
					args = append(args, fillProbe(field.Type.In(j)))
				}
				out := fn.Call(args)
				err, _ := out[1].Interface().(error)
				if err == nil || err.Error() != errLoginCancelled.Error() {
					found[field.Name] = "answered a cancelled prompt with (" + out[0].String() + ", " + errorText(err) + "), not \"Login cancelled\""
				}
			}
			// A prompt callback without a context blocks on the user with no way to cancel it; the host bridge prefers the Context form.
		}
	}}
	mode := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: t.TempDir()})
	if err := mode.runLoginRegisteredOAuth(t.Context(), provider, ""); err != nil {
		t.Fatal(err)
	}
	<-provider.done
	for _, name := range slices.Sorted(maps.Keys(found)) {
		problem := found[name]
		switch {
		case loginVariantExpectedRed[name] != "":
			t.Logf("expected red (%s): %s %s", loginVariantExpectedRed[name], name, problem)
		case loginVariantRoutedFindings[name] != "":
			t.Logf("routed finding (%s): %s %s", loginVariantRoutedFindings[name], name, problem)
		default:
			t.Errorf("registered OAuth login callback %s %s", name, problem)
		}
	}
	for _, list := range []map[string]string{loginVariantExpectedRed, loginVariantRoutedFindings} {
		for name, reason := range list {
			if found[name] == "" {
				t.Errorf("%s (%s) no longer fails: delete the entry", name, reason)
			}
		}
	}
}

func errorText(err error) string {
	if err == nil {
		return "nil"
	}
	return strings.ReplaceAll(err.Error(), "\n", " ")
}
