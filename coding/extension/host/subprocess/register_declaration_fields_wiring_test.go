package subprocess

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// registryDump renders what the registry holds as comparable text: exported fields only, a function as "func" or "nil", so two builds differ exactly
// where the registry differs.
func registryDump(value reflect.Value) string {
	switch value.Kind() {
	case reflect.Func:
		if value.IsNil() {
			return "nil"
		}
		return "func"
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return "nil"
		}
		return "&" + registryDump(value.Elem())
	case reflect.Struct:
		var parts []string
		for i := range value.NumField() {
			if value.Type().Field(i).IsExported() {
				parts = append(parts, value.Type().Field(i).Name+"="+registryDump(value.Field(i)))
			}
		}
		return "{" + strings.Join(parts, ",") + "}"
	case reflect.Slice, reflect.Array:
		var parts []string
		for i := range value.Len() {
			parts = append(parts, registryDump(value.Index(i)))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case reflect.Map:
		var parts []string
		for _, key := range value.MapKeys() {
			parts = append(parts, registryDump(key)+":"+registryDump(value.MapIndex(key)))
		}
		slices.Sort(parts)
		return "map[" + strings.Join(parts, ",") + "]"
	}
	return fmt.Sprintf("%v", value.Interface())
}

// fillWire sets a declaration field the way fillDeclaration does, with valid JSON in the raw-message fields the host decodes.
func fillWire(value reflect.Value, path string) {
	if value.Type() == reflect.TypeOf(json.RawMessage(nil)) {
		value.Set(reflect.ValueOf(json.RawMessage(fmt.Sprintf("%q", "probe-"+path))))
		return
	}
	if value.Kind() == reflect.Int {
		value.SetInt(7)
		return
	}
	fillDeclaration(value, path)
}

// registryFieldFor names the registry field that carries a declared string field when the names differ.
var registryFieldFor = map[string]string{"ShortcutDecl.Key": "Shortcut"}

// A registration declaration an SDK sends (tool, command, shortcut, flag) must reach the registry field by field (declaration -> carrier -> consumer,
// wiring finding wire:fcfde56f): setting any one declared field must change what the host registers. A field that changes nothing is dropped between
// the wire and the registry, so an SDK that sets it is silently ignored. A string field must also land in the registry field of the same name (or
// the one registryFieldFor names), so two fields swapped on the way in fail too. CommandDecl.Args ("argument hint shown in /help") was such a field: no SDK
// sent it, the host never read it, and Pi's registerCommand has no such option, so it was deleted.
func TestRegistrationDeclarationFieldsReachTheRegistry(t *testing.T) {
	host := NewHost(t.TempDir())
	build := func(reg *RegisterPayload) *extension.Extension {
		return host.buildExtension(&managedExt{config: ExtConfig{Name: "probe", Path: "/extensions/probe"}, host: host}, reg)
	}
	type kind struct {
		name    string
		decl    func() reflect.Value
		payload func(decl reflect.Value) *RegisterPayload
		dump    func(*extension.Extension, *RegisterPayload) string
	}
	kinds := []kind{
		{"ToolDecl", func() reflect.Value { return reflect.ValueOf(&ToolDecl{Name: "t"}).Elem() },
			func(d reflect.Value) *RegisterPayload {
				return &RegisterPayload{Tools: []ToolDecl{d.Interface().(ToolDecl)}}
			},
			func(e *extension.Extension, _ *RegisterPayload) string { return registryDump(reflect.ValueOf(e.Tools)) }},
		{"CommandDecl", func() reflect.Value { return reflect.ValueOf(&CommandDecl{Name: "c"}).Elem() },
			func(d reflect.Value) *RegisterPayload {
				return &RegisterPayload{Commands: []CommandDecl{d.Interface().(CommandDecl)}}
			},
			func(e *extension.Extension, _ *RegisterPayload) string {
				return registryDump(reflect.ValueOf(e.Commands))
			}},
		{"ShortcutDecl", func() reflect.Value { return reflect.ValueOf(&ShortcutDecl{Key: "ctrl+k"}).Elem() },
			func(d reflect.Value) *RegisterPayload {
				return &RegisterPayload{Shortcuts: []ShortcutDecl{d.Interface().(ShortcutDecl)}}
			},
			func(e *extension.Extension, _ *RegisterPayload) string {
				return registryDump(reflect.ValueOf(e.Shortcuts))
			}},
		{"VirtualModelDecl", func() reflect.Value {
			return reflect.ValueOf(&VirtualModelDecl{Provider: "p", ID: "m", Name: "n"}).Elem()
		},
			func(d reflect.Value) *RegisterPayload {
				return &RegisterPayload{VirtualModels: []VirtualModelDecl{d.Interface().(VirtualModelDecl)}}
			},
			func(_ *extension.Extension, reg *RegisterPayload) string {
				return registryDump(reflect.ValueOf(host.virtualModelDefinition(&managedExt{config: ExtConfig{Name: "probe"}, host: host}, reg.VirtualModels[0])))
			}},
		{"FlagDecl", func() reflect.Value { return reflect.ValueOf(&FlagDecl{Name: "f"}).Elem() },
			func(d reflect.Value) *RegisterPayload {
				return &RegisterPayload{Flags: []FlagDecl{d.Interface().(FlagDecl)}}
			},
			func(e *extension.Extension, _ *RegisterPayload) string { return registryDump(reflect.ValueOf(e.Flags)) }},
	}
	for _, k := range kinds {
		dump := func(reg *RegisterPayload) string { return k.dump(build(reg), reg) }
		base := dump(k.payload(k.decl()))
		typ := k.decl().Type()
		for i := range typ.NumField() {
			field := typ.Field(i)
			if !field.IsExported() {
				continue
			}
			decl := k.decl()
			fillWire(decl.Field(i), field.Name)
			got := dump(k.payload(decl))
			if got == base {
				t.Errorf("%s.%s set on the wire changes nothing in the registry: the host drops it", k.name, field.Name)
				continue
			}
			if field.Type.Kind() == reflect.String {
				target := field.Name
				if renamed, ok := registryFieldFor[k.name+"."+field.Name]; ok {
					target = renamed
				}
				if want := target + "=probe-" + field.Name; !strings.Contains(got, want) {
					t.Errorf("%s.%s reaches the registry but not as %s: %s", k.name, field.Name, want, got)
				}
			}
		}
	}
}
