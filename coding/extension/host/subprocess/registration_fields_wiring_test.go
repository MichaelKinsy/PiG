package subprocess

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// registrationListsElsewhere names each RegisterPayload declaration list whose fields another wiring test follows, with that test. Every other declaration list goes through registrationPaths.
var registrationListsElsewhere = map[string]string{
	"Providers": "TestNativeProviderDeclarationReachesTheCarrier and internal/codingagent TestProviderConfigRegistrationKeepsEveryField",
}

// registrationFieldExpectedRed lists the declaration fields a registration path drops today, keyed "<path> <Decl>.<Field>". An entry whose field survives fails.
var registrationFieldExpectedRed = map[string]string{
	// F15: SDKs send CanBlock, the host never reads it, and Pi has no blocking flag (every handler may return a result, runner.ts).
	"register HandlerDecl.CanBlock": "F15: dead wire field, Node, Python and Rust send can_block",
	"restart HandlerDecl.CanBlock":  "F15: dead wire field",
	// F16: a restarted isolated extension and a recovered packed member take only their new handlers and tools (adoptRestartedExtension); their new commands, shortcuts and flags keep the first registration's values.
	"restart CommandDecl.Description":         "F16: restart keeps the first registration's commands",
	"restart CommandDecl.ArgumentCompletions": "F16: restart keeps the first registration's commands",
	"restart ShortcutDecl.Description":        "F16: restart keeps the first registration's shortcuts",
	"restart FlagDecl.Description":            "F16: restart keeps the first registration's flags",
	"restart FlagDecl.Type":                   "F16: restart keeps the first registration's flags",
	"restart FlagDecl.Default":                "F16: restart keeps the first registration's flags",
}

// registrationPath applies one declaration through one production path and returns everything the host retained from it.
type registrationPath struct {
	name  string
	lists []string // RegisterPayload fields the path accepts
	// valid replaces the probe value of a field the path validates with a valid one, keyed "<Decl>.<Field>".
	valid map[string]any
	// apply returns everything the host retained, or the host's rejection.
	apply func(t *testing.T, list string, decl reflect.Value) (string, error)
}

func registrationPaths() []registrationPath {
	newExt := func() (*Host, *managedExt) {
		h := NewHost("")
		return h, &managedExt{config: ExtConfig{Name: "fields", Path: "/extensions/fields"}, host: h}
	}
	payload := func(list string, decl reflect.Value) *RegisterPayload {
		reg := &RegisterPayload{Name: "fields"}
		field := reflect.ValueOf(reg).Elem().FieldByName(list)
		field.Set(reflect.Append(field, decl))
		return reg
	}
	built := []string{"Tools", "Commands", "Shortcuts", "Handlers", "Flags", "MessageRenderers", "EntryRenderers"}
	return []registrationPath{{
		// adoptConn (host.go) and the packed handshake (packed_go.go) build the extension from the register payload.
		name:  "register",
		lists: built,
		apply: func(_ *testing.T, list string, decl reflect.Value) (string, error) {
			h, me := newExt()
			return renderRetained(reflect.ValueOf(h.buildExtension(me, payload(list, decl)))), nil
		},
	}, {
		// handleToolRegistration serves pi.registerTool after load (loader.ts:289-301).
		name:  "registerTool",
		lists: []string{"Tools"},
		apply: func(t *testing.T, _ string, decl reflect.Value) (string, error) {
			h, me := newExt()
			me.ext = h.buildExtension(me, &RegisterPayload{Name: "fields"})
			h.exts["fields"] = me
			args, err := json.Marshal(decl.Interface())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.handleToolRegistration(t.Context(), me, me.connection(), &CallPayload{Method: CallRegisterTool, Args: args}); err != nil {
				return "", err
			}
			return renderRetained(reflect.ValueOf(me.ext.RegisteredTools())), nil
		},
	}, {
		// A restarted isolated extension (adoptConn) and a member of a recovered packed cell (packed_go.go handshake with recoveryMembersKey) keep the Extension the runner holds and take the new registration into it through adoptRestartedExtension, so the two agree as D20 requires.
		name:  "restart",
		lists: built,
		apply: func(_ *testing.T, list string, decl reflect.Value) (string, error) {
			return renderRetained(reflect.ValueOf(reregister(list, decl, adoptRestartedExtension))), nil
		},
	}, {
		// registerExtensionAPI registers a load-time virtual model with the runtime, and handleVirtualModelCall a later one (loader.ts:500-509).
		name:  "registerVirtualModel",
		lists: []string{"VirtualModels"},
		apply: func(t *testing.T, list string, decl reflect.Value) (string, error) {
			h, me := newExt()
			if err := h.registerExtensionAPI(me, payload(list, decl)); err != nil {
				return "", err
			}
			load := renderRetained(reflect.ValueOf(h.providerRuntime.PendingVirtualModelRegistrations())) + renderRetained(reflect.ValueOf(me.virtualModels))
			h, me = newExt()
			args, err := json.Marshal(decl.Interface())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.handleVirtualModelCall(t.Context(), me, &CallPayload{Method: CallRegisterVirtualModel, Args: args}); err != nil {
				return "", err
			}
			later := renderRetained(reflect.ValueOf(h.providerRuntime.PendingVirtualModelRegistrations())) + renderRetained(reflect.ValueOf(me.virtualModels))
			if later != load {
				t.Errorf("registerVirtualModel: a model registered after load is retained differently from one registered while loading:\n%s\n%s", load, later)
			}
			return load, nil
		},
	}, {
		// registerExtensionAPI registers a load-time MCP server with the runtime, and handleMcpServerCall a later one (loader.ts:471-488).
		name:  "registerMcpServer",
		lists: []string{"McpServers"},
		valid: map[string]any{"McpServerDecl.Name": "probe-mcp", "McpServerDecl.Config": json.RawMessage(`{"command":"probe-McpServerDecl.Config"}`)},
		apply: func(t *testing.T, list string, decl reflect.Value) (string, error) {
			h, me := newExt()
			if err := h.registerExtensionAPI(me, payload(list, decl)); err != nil {
				return "", err
			}
			load := renderRetained(reflect.ValueOf(h.providerRuntime.McpServers())) + renderRetained(reflect.ValueOf(me.mcpServerNames))
			h, me = newExt()
			args, err := json.Marshal(decl.Interface())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.handleMcpServerCall(t.Context(), me, &CallPayload{Method: CallRegisterMcpServer, Args: args}); err != nil {
				return "", err
			}
			later := renderRetained(reflect.ValueOf(h.providerRuntime.McpServers())) + renderRetained(reflect.ValueOf(me.mcpServerNames))
			if later != load {
				t.Errorf("registerMcpServer: a server registered after load is retained differently from one registered while loading:\n%s\n%s", load, later)
			}
			return load, nil
		},
	}}
}

// reregister builds the extension from a registration that carries only the declaration's key, then from the full declaration, and adopts the second into the first as a restart or recovery does.
func reregister(list string, decl reflect.Value, adopt func(previous, built *extension.Extension) *extension.Extension) *extension.Extension {
	h := NewHost("")
	me := &managedExt{config: ExtConfig{Name: "fields", Path: "/extensions/fields"}, host: h}
	payload := func(entry reflect.Value) *RegisterPayload {
		reg := &RegisterPayload{Name: "fields"}
		field := reflect.ValueOf(reg).Elem().FieldByName(list)
		field.Set(reflect.Append(field, entry))
		return reg
	}
	stale := reflect.New(decl.Type()).Elem()
	stale.FieldByName(declKeyField(decl.Type())).Set(decl.FieldByName(declKeyField(decl.Type())))
	previous := h.buildExtension(me, payload(stale))
	return adopt(previous, h.buildExtension(me, payload(decl)))
}

// declKeyField is the field that names a declaration in its list; a restart keeps it so the stale entry is the same registration.
func declKeyField(typ reflect.Type) string {
	for _, name := range []string{"Name", "Key", "CustomType", "Event"} {
		if _, ok := typ.FieldByName(name); ok {
			return name
		}
	}
	return typ.Field(0).Name
}

// TestRegistrationDeclarationsKeepEveryField fills every field of every declaration list an extension registers, applies it through every registration path, and checks that clearing any one field changes what the host retained. A field whose value never shows is dropped.
func TestRegistrationDeclarationsKeepEveryField(t *testing.T) {
	paths := registrationPaths()
	accepted := map[string]bool{}
	for _, path := range paths {
		for _, list := range path.lists {
			accepted[list] = true
		}
	}
	survived := map[string]bool{}
	payloadType := reflect.TypeFor[RegisterPayload]()
	for i := range payloadType.NumField() {
		list := payloadType.Field(i)
		if list.Type.Kind() != reflect.Slice || list.Type.Elem().Kind() != reflect.Struct {
			continue
		}
		if _, elsewhere := registrationListsElsewhere[list.Name]; elsewhere {
			continue
		}
		if !accepted[list.Name] {
			if strings.HasSuffix(list.Type.Elem().Name(), "Ref") {
				continue // an unregistration names an entry and carries no fields to keep
			}
			t.Errorf("RegisterPayload.%s ([]%s) has no registration path in registrationPaths", list.Name, list.Type.Elem().Name())
		}
	}
	for _, path := range paths {
		for _, list := range path.lists {
			field, ok := payloadType.FieldByName(list)
			if !ok {
				t.Errorf("path %s names RegisterPayload.%s, which does not exist", path.name, list)
				continue
			}
			declType := field.Type.Elem()
			full := fillRegistration(declType, declType.Name())
			for name, value := range path.valid {
				if strings.HasPrefix(name, declType.Name()+".") {
					full.FieldByName(strings.TrimPrefix(name, declType.Name()+".")).Set(reflect.ValueOf(value))
				}
			}
			want, err := path.apply(t, list, full)
			if err != nil {
				t.Errorf("%s rejects a fully filled %s: %v", path.name, declType.Name(), err)
				continue
			}
			for j := range declType.NumField() {
				member := declType.Field(j)
				if !member.IsExported() {
					continue
				}
				id := path.name + " " + declType.Name() + "." + member.Name
				cleared := reflect.New(declType).Elem()
				cleared.Set(full)
				cleared.Field(j).SetZero()
				if got, err := path.apply(t, list, cleared); err != nil || got != want {
					survived[id] = true // retained, or validated and required
					continue
				}
				if _, red := registrationFieldExpectedRed[id]; !red {
					t.Errorf("%s: the host keeps nothing of %s.%s (clearing it changes no retained state)", path.name, declType.Name(), member.Name)
				}
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(registrationFieldExpectedRed)) {
		if survived[id] {
			t.Errorf("registrationFieldExpectedRed[%q] (%s) survives now: delete the entry", id, registrationFieldExpectedRed[id])
		}
	}
}

// fillRegistration gives every field a value distinct from its zero value. Raw JSON is an object, so schemas and flag defaults stay valid.
func fillRegistration(typ reflect.Type, path string) reflect.Value {
	v := reflect.New(typ).Elem()
	switch typ.Kind() {
	case reflect.String:
		v.SetString("probe-" + path)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(7 + len(path)))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(7 + len(path)))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.5)
	case reflect.Slice:
		if typ.Elem().Kind() == reflect.Uint8 {
			v.SetBytes(fmt.Appendf(nil, `{"description":%q}`, "probe-"+path))
			break
		}
		v.Set(reflect.Append(v, fillRegistration(typ.Elem(), path+"[]")))
	case reflect.Map:
		v.Set(reflect.MakeMap(typ))
		v.SetMapIndex(fillRegistration(typ.Key(), path+".key"), fillRegistration(typ.Elem(), path+".value"))
	case reflect.Pointer:
		v.Set(reflect.New(typ.Elem()))
		v.Elem().Set(fillRegistration(typ.Elem(), path))
	case reflect.Struct:
		for i := range typ.NumField() {
			if typ.Field(i).IsExported() {
				v.Field(i).Set(fillRegistration(typ.Field(i).Type, path+"."+typ.Field(i).Name))
			}
		}
	case reflect.Interface:
		v.Set(reflect.ValueOf("probe-" + path))
	}
	return v
}

// renderRetained writes everything reachable from v, unexported state included, so a declared value the host keeps anywhere shows. A function renders only as set or nil.
func renderRetained(v reflect.Value) string {
	seen := map[uintptr]bool{}
	var walk func(reflect.Value) string
	walk = func(v reflect.Value) string {
		switch v.Kind() {
		case reflect.Invalid:
			return "invalid"
		case reflect.Func:
			if v.IsNil() {
				return "func(nil)"
			}
			return "func"
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return "nil"
			}
			if v.Kind() == reflect.Pointer {
				if seen[v.Pointer()] {
					return "cycle"
				}
				seen[v.Pointer()] = true
			}
			return walk(v.Elem())
		case reflect.Struct:
			parts := make([]string, v.NumField())
			for i := range v.NumField() {
				parts[i] = v.Type().Field(i).Name + ":" + walk(v.Field(i))
			}
			return v.Type().String() + "{" + strings.Join(parts, ",") + "}"
		case reflect.Slice, reflect.Array:
			parts := make([]string, v.Len())
			for i := range v.Len() {
				parts[i] = walk(v.Index(i))
			}
			return "[" + strings.Join(parts, ",") + "]"
		case reflect.Map:
			parts := make([]string, 0, v.Len())
			for _, key := range v.MapKeys() {
				parts = append(parts, walk(key)+"="+walk(v.MapIndex(key)))
			}
			slices.Sort(parts)
			return "map[" + strings.Join(parts, ";") + "]"
		case reflect.Chan, reflect.UnsafePointer:
			return v.Kind().String()
		default:
			return fmt.Sprint(v)
		}
	}
	return walk(v)
}
