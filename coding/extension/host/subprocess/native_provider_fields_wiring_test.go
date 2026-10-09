package subprocess

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// carrierFieldExpectedRed lists known drops open on this branch between a
// provider object's wire declaration and the registration carrier the host
// builds from it (nativeProviderProxy.auth); an entry whose field survives
// fails. It is empty since #201.
var carrierFieldExpectedRed = map[string]string{}

// declarationOnlyFields are declaration fields the host consumes itself
// instead of carrying them into the registry.
var declarationOnlyFields = map[string]string{
	"Key":     "the provider's callback key addresses its requests on the extension's connection",
	"Handle":  "the host-assigned handle addresses provider.object calls from other extensions",
	"Methods": "each method is checked below as the carrier member it becomes",
	"OAuth":   "publishNativeProvider registers the legacy OAuth login proxy from it",
}

// unsentDeclarationPaths are declaration leaves of a shared wire type that no
// SDK sends in that position.
var unsentDeclarationPaths = map[string]string{
	"Auth.APIKey.IsSubscription": "Pi's ApiKeyAuth has no isSubscription (auth/types.ts); the wire type is shared with OAuth",
	"Auth.APIKey.LoginLabel":     "Pi's ApiKeyAuth has no loginLabel (auth/types.ts); the wire type is shared with OAuth",
}

// declarableMethods reads the provider object methods the Node runtime can
// declare (native-provider.mjs nativeDeclaration), which every SDK shares.
func declarableMethods(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testenv.ModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node", "native-provider.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	list := regexp.MustCompile(`const methods = (\w+)\.filter`).FindStringSubmatch(text)
	if list == nil {
		t.Fatal("native-provider.mjs no longer filters a methods list; update this check")
	}
	body := regexp.MustCompile(`const ` + list[1] + ` = \[([^\]]*)\]`).FindStringSubmatch(text)
	if body == nil {
		t.Fatal("native-provider.mjs methods list not found; update this check")
	}
	var methods []string
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(body[1], -1) {
		methods = append(methods, m[1])
	}
	return methods
}

// fillDeclaration sets every leaf: strings to "probe-<path>", bools to true,
// pointers to filled values, maps and slices to one filled element.
func fillDeclaration(value reflect.Value, path string) {
	switch value.Kind() {
	case reflect.Pointer:
		value.Set(reflect.New(value.Type().Elem()))
		fillDeclaration(value.Elem(), path)
	case reflect.Struct:
		for i := range value.NumField() {
			if value.Type().Field(i).IsExported() {
				fillDeclaration(value.Field(i), strings.TrimPrefix(path+"."+value.Type().Field(i).Name, "."))
			}
		}
	case reflect.String:
		value.SetString("probe-" + path)
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Map:
		value.Set(reflect.MakeMap(value.Type()))
		key, elem := reflect.New(value.Type().Key()).Elem(), reflect.New(value.Type().Elem()).Elem()
		fillDeclaration(key, path+".key")
		fillDeclaration(elem, path)
		value.SetMapIndex(key, elem)
	case reflect.Slice:
		elem := reflect.New(value.Type().Elem()).Elem()
		fillDeclaration(elem, path)
		value.Set(reflect.Append(reflect.MakeSlice(value.Type(), 0, 1), elem))
	}
}

// carrierPath resolves a dotted path in the carrier, matching each segment case-insensitively.
func carrierPath(value reflect.Value, path string) (reflect.Value, bool) {
	for segment := range strings.SplitSeq(path, ".") {
		for value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return reflect.Value{}, false
			}
			value = value.Elem()
		}
		if value.Kind() != reflect.Struct {
			return reflect.Value{}, false
		}
		field, ok := value.Type().FieldByNameFunc(func(name string) bool { return strings.EqualFold(name, segment) })
		if !ok {
			return reflect.Value{}, false
		}
		value = value.FieldByIndex(field.Index)
	}
	return value, true
}

// wireValue renders a value as JSON after dereferencing, so a *string and a
// string, or a header map and ai.ProviderHeaders, compare by content.
func wireValue(value reflect.Value) string {
	for value.Kind() == reflect.Pointer && !value.IsNil() {
		value = value.Elem()
	}
	data, err := json.Marshal(value.Interface())
	if err != nil {
		return fmt.Sprintf("%v", value.Interface())
	}
	return string(data)
}

// Every field an extension declares for a provider object reaches the
// registration carrier the host hands to the registry, and every declared
// method becomes the carrier member of the same name (auth.apiKey.login is
// Auth.APIKey.Login). A field the carrier drops cannot reach /login, the model
// catalog or a request, as #201's loginLabel and logins could not.
func TestNativeProviderDeclarationReachesTheCarrier(t *testing.T) {
	var declaration NativeProviderDeclaration
	fillDeclaration(reflect.ValueOf(&declaration).Elem(), "")
	declaration.Methods = declarableMethods(t)
	h := &Host{}
	p := &nativeProviderProxy{host: h, declaration: declaration, publications: map[string]nativeProviderCallback{}}
	carrier := reflect.ValueOf(p.carrier()).Elem()

	found := map[string]string{}
	var walk func(value reflect.Value, path string)
	walk = func(value reflect.Value, path string) {
		switch value.Kind() {
		case reflect.Pointer:
			walk(value.Elem(), path)
			return
		case reflect.Struct:
			if value.Type() != reflect.TypeFor[extension.ProviderModelConfig]() {
				for i := range value.NumField() {
					if value.Type().Field(i).IsExported() {
						walk(value.Field(i), strings.TrimPrefix(path+"."+value.Type().Field(i).Name, "."))
					}
				}
				return
			}
		}
		if _, consumed := declarationOnlyFields[strings.Split(path, ".")[0]]; consumed {
			return
		}
		if _, unsent := unsentDeclarationPaths[path]; unsent {
			return
		}
		got, ok := carrierPath(carrier, path)
		switch {
		case !ok:
			found[path] = "has no carrier field"
		case wireValue(got) != wireValue(value):
			found[path] = fmt.Sprintf("is %s in the carrier, declared %s", wireValue(got), wireValue(value))
		}
	}
	walk(reflect.ValueOf(declaration), "")
	for _, method := range declaration.Methods {
		got, ok := carrierPath(carrier, method)
		switch {
		case !ok:
			found["method "+method] = "has no carrier member"
		case got.Kind() != reflect.Func:
			found["method "+method] = "maps to a carrier field that is not a function"
		case got.IsNil():
			found["method "+method] = "is declared, but its carrier member is nil"
		}
	}
	for _, path := range slices.Sorted(maps.Keys(found)) {
		if reason := carrierFieldExpectedRed[path]; reason != "" {
			t.Logf("expected red (%s): %s %s", reason, path, found[path])
			continue
		}
		t.Errorf("declaration %s %s", path, found[path])
	}
	for path, reason := range carrierFieldExpectedRed {
		if found[path] == "" {
			t.Errorf("carrierFieldExpectedRed[%q] (%s) survives now: delete the entry", path, reason)
		}
	}
	for path, reason := range unsentDeclarationPaths {
		if _, ok := carrierPath(reflect.ValueOf(declaration), path); !ok {
			t.Errorf("unsentDeclarationPaths[%q] (%s) names no declaration field", path, reason)
		}
	}
	for field, reason := range declarationOnlyFields {
		if _, ok := reflect.TypeFor[NativeProviderDeclaration]().FieldByName(field); !ok {
			t.Errorf("declarationOnlyFields[%q] (%s) names no declaration field", field, reason)
		}
	}
}
