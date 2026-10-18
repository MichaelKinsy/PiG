package host

// Ports packages/chord/src/contract.ts. The TypeScript static types (ImplementationOf, MembersOf, InputOf, Implementation,
// Call) have no Go form; a Go implementation is a Implementation object and a reference reaches members by name.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// TypeError is a contract definition error, thrown where upstream throws a TypeError. Remote, Local and Opaque panic
// with it: contracts are package-level definitions, as upstream's are module-level constants.
type TypeError struct{ Message string }

func (err *TypeError) Error() string { return err.Message }

func typeError(format string, args ...any) *TypeError {
	return &TypeError{Message: fmt.Sprintf(format, args...)}
}

// MethodSpec is a method member: its arguments and result schemas. A nil Args means the method takes no arguments (it
// is called `ref.name(ctx)`; in Go its handler is still a methodFunc, whose args are nil), a nil Result that it returns
// nothing (void).
type MethodSpec struct {
	Args   Schema
	Result Schema
}

// OpaqueSpec is a native in-process object identified by name@version (local contracts only).
type OpaqueSpec struct{ ID string }

// Method is contract.ts method(args?, result?) (contract.ts:31-33, three overloads): `method()` is Method(), `method(args)`
// is Method(args) (no result, the handler returns nothing) and `method(undefined, result)` is Method(nil, result). Go has
// no overloads, so the parameters are variadic: a nil schema is Pi's undefined, and more than two panic with a TypeError.
func Method(schemas ...Schema) MethodSpec {
	if len(schemas) > 2 {
		panic(typeError("method takes at most args and result, got %d schemas", len(schemas)))
	}
	var spec MethodSpec
	if len(schemas) > 0 {
		spec.Args = schemas[0]
	}
	if len(schemas) > 1 {
		spec.Result = schemas[1]
	}
	return spec
}

// takesNoArgs reports whether contract's method name takes no arguments (it is called as `ref.name(ctx)`).
func takesNoArgs(contract *Contract, name string) bool {
	for _, variant := range contract.Description.Variants {
		if member, ok := variant.Value.Get(name); ok && member.Kind == MemberMethod {
			return member.Args == nil
		}
	}
	return false
}

var opaqueID = regexp.MustCompile(`^[^@\s]+@\d+$`)

// Opaque is contract.ts opaque(id); it panics with a TypeError when id is not name@version.
func Opaque(id string) OpaqueSpec {
	if !opaqueID.MatchString(id) {
		panic(typeError("opaque id must be name@version: %s", id))
	}
	return OpaqueSpec{ID: id}
}

// Field is one own property of a JavaScript object literal: its name and value.
type Field[V any] struct {
	Name  string
	Value V
}

// Fields is a JavaScript object literal as an ordered list of properties. Pi iterates such objects (Object.entries,
// Object.keys) and JSON.stringify writes them in own-key order: array-index names first in ascending order, then the
// other names in insertion order. Contracts normalize their definitions to that order (ownFields).
type Fields[V any] []Field[V]

// Get is the value of property name.
func (fields Fields[V]) Get(name string) (V, bool) {
	for _, field := range fields {
		if field.Name == name {
			return field.Value, true
		}
	}
	var zero V
	return zero, false
}

// Has reports whether property name exists.
func (fields Fields[V]) Has(name string) bool {
	_, ok := fields.Get(name)
	return ok
}

// ownFields is fields as a JavaScript object built from them: properties in own-key order, and a repeated name keeps
// its first position and takes the last value (as assigning a property again does).
func ownFields[V any](fields Fields[V]) Fields[V] {
	object := chordjson.NewObject(len(fields))
	for _, field := range fields {
		object.Set(field.Name, field.Value)
	}
	out := make(Fields[V], 0, object.Len())
	for name, value := range object.All() {
		out = append(out, Field[V]{Name: name, Value: as[V](value)})
	}
	return out
}

// VariantDef is one variant of a remote contract: its state members, then its methods, each in declaration order.
type VariantDef struct {
	State   Fields[Schema]
	Methods Fields[MethodSpec]
}

// Placement is a remote type's placement (contract.ts Placement = HostNamed | HostFamily): where its objects live, as
// data, so a description carries it and every host can check it. It is a closed union of HostNamed and HostFamily.
type Placement interface{ isPlacement() }

// HostNamed places the objects of a remote type on the one host named Name (contract.ts HostNamed).
type HostNamed struct{ Name string }

// HostFamily places the objects of a remote type on the host Prefix + the value of the input's string field Key
// (contract.ts HostFamily): `session:` + `session` puts `{ session: "S1", … }` on `session:S1`.
type HostFamily struct{ Prefix, Key string }

// PlacementFor is contract.ts PlacementFor<I> = HostNamed | HostFamily<StringKeys<I>>: the placements a remote type with
// input I may have. The TypeScript parameter restricts a family's key to I's required string fields at compile time;
// Go's input is untyped, so the same restriction is the runtime check resolveHost makes (invalid/placement when the
// field names no host), and the type is Placement.
type PlacementFor = Placement

func (HostNamed) isPlacement()  {}
func (HostFamily) isPlacement() {}

// NewHostNamed is contract.ts hostNamed(name): objects of the type live on the host named name. It panics with a
// TypeError for an empty name.
func NewHostNamed(name string) HostNamed {
	if name == "" {
		panic(typeError("hostNamed needs a host name"))
	}
	return HostNamed{Name: name}
}

// NewHostFamily is contract.ts hostFamily(prefix, key): objects of the type live on prefix + the value of the input's
// string field key. The field is required and must be a non-empty string. It panics with a TypeError for an empty prefix
// or key.
func NewHostFamily(prefix, key string) HostFamily {
	if prefix == "" {
		panic(typeError("hostFamily needs a host-name prefix"))
	}
	if key == "" {
		panic(typeError("hostFamily needs an input field name"))
	}
	return HostFamily{Prefix: prefix, Key: key}
}

// resolveHost is contract.ts resolveHost: the host an object of placement with this (normalized) input lives on. It
// fails invalid/placement when a family's field names no host.
func resolveHost(typeName string, placement Placement, input any) (string, error) {
	switch typed := placement.(type) {
	case HostNamed:
		return typed.Name, nil
	case HostFamily:
		var value any = absent{}
		if field, ok := principalFields(input); ok {
			value = field(typed.Key)
		}
		if text, ok := value.(string); ok && text != "" {
			return typed.Prefix + text, nil
		}
		return "", Invalid("placement", fmt.Sprintf("%s: input field %s names no host", typeName, typed.Key), nil)
	}
	return "", Invalid("placement", typeName+" has no placement", nil)
}

// placedOn is contract.ts placedOn: whether the host named hostName may serve a type of placement.
func placedOn(placement Placement, hostName string) bool {
	switch typed := placement.(type) {
	case HostNamed:
		return typed.Name == hostName
	case HostFamily:
		return strings.HasPrefix(hostName, typed.Prefix) && len(hostName) > len(typed.Prefix)
	}
	return false
}

// placementText is contract.ts placementText.
func placementText(placement Placement) string {
	switch typed := placement.(type) {
	case HostNamed:
		return typed.Name
	case HostFamily:
		return typed.Prefix + "<" + typed.Key + ">"
	}
	return ""
}

// RemoteDef defines a remote contract. Variants are in declaration order. Host is required: where its objects live.
type RemoteDef struct {
	Version  int
	Input    Schema
	Host     Placement
	Variants Fields[VariantDef]
}

// LocalDef defines a local contract. A member is a MethodSpec, an OpaqueSpec or a state Schema, in declaration order.
type LocalDef struct {
	Version int
	Input   Schema
	Members Fields[any]
}

// Member kinds of a MemberDescription.
const (
	MemberMethod = "method"
	MemberState  = "state"
	MemberOpaque = "opaque"
)

// MemberDescription describes one member: a method (Args, Result), a state (Schema) or an opaque object (ID).
type MemberDescription struct {
	Kind   string
	Args   Schema
	Result Schema
	Schema Schema
	ID     string
}

// LocalVariant is the one unnamed variant of a local contract.
const LocalVariant = ""

// Description is a contract's canonical description: what a token carries over a link and what fingerprints hash.
// Variants and their members are in Pi's order (own-key order of the definition).
type Description struct {
	Name    string
	Scope   ContractScope
	Version int
	Input   Schema
	// Host is where the objects of a remote type live (required there); nil for a local type.
	Host     Placement
	Variants Fields[Fields[MemberDescription]]
}

// A description travels as a hold's contract token (wire.go), so it is declared once more as the schema that validates
// it (contract.ts DescriptionSchema). A schema member is checked only for being an object (contract.ts SchemaObject).
var (
	schemaObject            = object(nil)
	memberDescriptionSchema = Schema{"anyOf": []any{
		// Absent args: the method takes none. Absent result: it returns nothing.
		object([]string{"kind"}, "kind", literal(MemberMethod), "args", schemaObject, "result", schemaObject),
		object([]string{"kind", "schema"}, "kind", literal(MemberState), "schema", schemaObject),
		object([]string{"kind", "id"}, "kind", literal(MemberOpaque), "id", Schema{"type": "string"}),
	}}
	// contract.ts PlacementSchema: a closed { name } or a closed { prefix, key }.
	placementSchema = Schema{"anyOf": []any{
		closed(object([]string{"name"}, "name", Schema{"type": "string", "minLength": 1})),
		closed(object([]string{"prefix", "key"}, "prefix", Schema{"type": "string", "minLength": 1}, "key", Schema{"type": "string", "minLength": 1})),
	}}
	descriptionSchema = object([]string{"name", "scope", "version", "input", "variants"},
		"name", Schema{"type": "string", "minLength": 1},
		"scope", Schema{"anyOf": []any{literal(string(ScopeRemote)), literal(string(ScopeLocal))}},
		"version", integer(),
		"input", schemaObject,
		// Remote types only (required there): where their objects live.
		"host", placementSchema,
		"variants", record(record(memberDescriptionSchema)),
	)
)

// member is the description of member name in variant kind.
func (description Description) member(kind, name string) (MemberDescription, bool) {
	members, ok := description.Variants.Get(kind)
	if !ok {
		return MemberDescription{}, false
	}
	return members.Get(name)
}

// ContractScope is a contract's scope, the discriminator of contract.ts AnyContract = RemoteContract | LocalContract.
type ContractScope string

// Contract scopes.
const (
	ScopeRemote ContractScope = "remote"
	ScopeLocal  ContractScope = "local"
)

// Contract is a remote or local contract token (contract.ts RemoteContract | LocalContract). A token received over a
// link has neither Remote nor Local definition members: it is only good for binding and validation.
type Contract struct {
	Scope       ContractScope
	Name        string
	Version     int
	Input       Schema
	Description Description
	Fingerprint string
	Remote      *RemoteDef
	Local       *LocalDef
	// decoded marks a token whose schemas came over a link as JSON. TypeBox's Clean only acts on schemas it built
	// itself (their "~kind"), so a decoded token strips nothing.
	decoded bool
}

// reservedMembers are names a reference uses itself; members may not use them.
var reservedMembers = map[string]bool{
	"variant": true, "status": true, "ready": true, "shape": true, "error": true, "onShape": true, "onStatus": true,
	"release": true, "then": true, "contract": true,
}

func describeMember(name string, member any, scope ContractScope) MemberDescription {
	if reservedMembers[name] {
		panic(typeError("member name is reserved: %s", name))
	}
	switch typed := member.(type) {
	case MethodSpec:
		return MemberDescription{Kind: MemberMethod, Args: typed.Args, Result: typed.Result}
	case OpaqueSpec:
		if scope == ScopeRemote {
			panic(typeError("remote contracts cannot expose opaque members: %s", name))
		}
		return MemberDescription{Kind: MemberOpaque, ID: typed.ID}
	case Schema:
		return MemberDescription{Kind: MemberState, Schema: typed}
	}
	panic(typeError("member %s is not a method(), opaque() or schema", name))
}

func checkHeader(name string, version int) {
	if name == "" {
		panic(typeError("contract name is empty"))
	}
	if version < 1 {
		panic(typeError("contract version must be a positive integer"))
	}
}

func finish(contract *Contract) *Contract {
	contract.Fingerprint = Hash64(CanonicalJSON(contract.Description))
	return contract
}

// Remote is contract.ts remote(name, def). It panics with a TypeError on an invalid definition. Variants and members
// are described in Pi's order: each variant's state members, then its methods.
func Remote(name string, def RemoteDef) *Contract {
	checkHeader(name, def.Version)
	host := copyPlacement(name, def.Host)
	if len(def.Variants) == 0 {
		panic(typeError("remote contract %s has no variants", name))
	}
	var variants Fields[Fields[MemberDescription]]
	for _, variant := range ownFields(def.Variants) {
		kind := variant.Name
		var members Fields[MemberDescription]
		for _, state := range ownFields(variant.Value.State) {
			members = append(members, Field[MemberDescription]{Name: state.Name, Value: describeMember(state.Name, state.Value, ScopeRemote)})
		}
		for _, method := range ownFields(variant.Value.Methods) {
			if members.Has(method.Name) {
				panic(typeError("member %s is both state and method in %s.%s", method.Name, name, kind))
			}
			members = append(members, Field[MemberDescription]{Name: method.Name, Value: describeMember(method.Name, method.Value, ScopeRemote)})
		}
		variants = append(variants, Field[Fields[MemberDescription]]{Name: kind, Value: ownFields(members)})
	}
	description := Description{Name: name, Scope: ScopeRemote, Version: def.Version, Input: def.Input, Host: host, Variants: ownFields(variants)}
	copied := def
	copied.Host = host
	return finish(&Contract{Scope: ScopeRemote, Name: name, Version: def.Version, Input: def.Input, Remote: &copied, Description: description})
}

// copyPlacement is remote()'s check and copy of its `host`: a placement that is not valid by PlacementSchema is a
// TypeError, and the description carries a copy of it that holds nothing else.
func copyPlacement(name string, placement Placement) Placement {
	switch typed := placement.(type) {
	case HostNamed:
		if typed.Name != "" {
			return HostNamed{Name: typed.Name}
		}
	case HostFamily:
		if typed.Prefix != "" && typed.Key != "" {
			return HostFamily{Prefix: typed.Prefix, Key: typed.Key}
		}
	}
	panic(typeError("remote contract %s needs `host`: hostNamed(…) or hostFamily(…)", name))
}

// Local is contract.ts local(name, def). It panics with a TypeError on an invalid definition.
func Local(name string, def LocalDef) *Contract {
	checkHeader(name, def.Version)
	members := Fields[MemberDescription]{}
	for _, member := range ownFields(def.Members) {
		members = append(members, Field[MemberDescription]{Name: member.Name, Value: describeMember(member.Name, member.Value, ScopeLocal)})
	}
	description := Description{Name: name, Scope: ScopeLocal, Version: def.Version, Input: def.Input, Variants: Fields[Fields[MemberDescription]]{{Name: LocalVariant, Value: members}}}
	copied := def
	return finish(&Contract{Scope: ScopeLocal, Name: name, Version: def.Version, Input: def.Input, Local: &copied, Description: description})
}

// contractFromDescription is a consumer token received over a link (only its description travels).
func contractFromDescription(description Description) (*Contract, error) {
	if (description.Scope != ScopeRemote && description.Scope != ScopeLocal) || (description.Scope == ScopeRemote) != (description.Host != nil) {
		return nil, NewChordError(CodeInvalid, "malformed contract token", ChordErrorOptions{Reason: "token"})
	}
	return &Contract{
		Scope:       description.Scope,
		Name:        description.Name,
		Version:     description.Version,
		Input:       description.Input,
		Description: description,
		Fingerprint: Hash64(CanonicalJSON(description)),
		decoded:     true,
	}, nil
}

// Coverage is contract.ts coverage: why provider does not cover consumer, or "" when it does.
func Coverage(provider, consumer Description) string {
	if provider.Name != consumer.Name || provider.Scope != consumer.Scope {
		return "different type"
	}
	if !samePlacement(provider, consumer) {
		return "host"
	}
	if !Fits(consumer.Input, provider.Input) {
		return "input"
	}
	for _, consumerVariant := range consumer.Variants {
		kind := consumerVariant.Name
		providerMembers, ok := provider.Variants.Get(kind)
		if !ok {
			return fmt.Sprintf("variant %s missing", kind)
		}
		for _, consumerMember := range consumerVariant.Value {
			name, wanted := consumerMember.Name, consumerMember.Value
			offered, ok := providerMembers.Get(name)
			if !ok || offered.Kind != wanted.Kind {
				return fmt.Sprintf("%s.%s missing or of another kind", kind, name)
			}
			switch offered.Kind {
			case MemberMethod:
				// Both take arguments or neither does; a consumer that expects no result accepts any.
				if (wanted.Args == nil) != (offered.Args == nil) {
					return fmt.Sprintf("%s.%s arguments", kind, name)
				}
				if wanted.Args != nil && offered.Args != nil && !Fits(wanted.Args, offered.Args) {
					return fmt.Sprintf("%s.%s arguments", kind, name)
				}
				if wanted.Result != nil && (offered.Result == nil || !Fits(offered.Result, wanted.Result)) {
					return fmt.Sprintf("%s.%s result", kind, name)
				}
			case MemberState:
				if !Fits(offered.Schema, wanted.Schema) {
					return fmt.Sprintf("%s.%s state", kind, name)
				}
			case MemberOpaque:
				if offered.ID != wanted.ID {
					return fmt.Sprintf("%s.%s opaque %s != %s", kind, name, offered.ID, wanted.ID)
				}
			}
		}
	}
	return ""
}

// samePlacement reports whether two descriptions place their objects equally (canonicalJson(a.host ?? null)).
func samePlacement(a, b Description) bool {
	return CanonicalJSON(a.placementValue()) == CanonicalJSON(b.placementValue())
}

// placementOf is the placement as the bind error names it: "(local)" for a local type.
func (description Description) placementOf() string {
	if description.Host == nil {
		return "(local)"
	}
	return placementText(description.Host)
}

// placementValue is the placement as data for canonicalJson: null when there is none.
func (description Description) placementValue() any {
	switch typed := description.Host.(type) {
	case HostNamed:
		return map[string]any{"name": typed.Name}
	case HostFamily:
		return map[string]any{"prefix": typed.Prefix, "key": typed.Key}
	}
	return nil
}

// Implementation is what a factory returns: members by name (methods, state sources, opaque values) and, for a remote
// contract, the variant under "kind". It is a JavaScript object: members keep own-key order (Shape.Members, validation
// order) and the object has identity (accepts views are cached per implementation).
type Implementation = *chordjson.Object

// Adapter maps the provider's current implementation to the view a consumer of an older contract version expects.
type Adapter func(current Implementation) Implementation

// Accepts are the adapters a provider declares, by consumer contract version.
type Accepts map[int]Adapter

// Bind is contract.ts bind: how a consumer token binds to a provider, directly (nil adapter) or through an accepts
// adapter. It fails with contract_mismatch.
func Bind(provider *Contract, accepts Accepts, consumer *Contract) (Adapter, error) {
	if provider.Fingerprint == consumer.Fingerprint {
		return nil, nil
	}
	// A moved type is another type for its consumers: no adapter reaches across hosts.
	if !samePlacement(provider.Description, consumer.Description) {
		return nil, NewChordError(CodeContractMismatch, fmt.Sprintf("%s lives on %s, the consumer expects %s", provider.Name, provider.Description.placementOf(), consumer.Description.placementOf()), ChordErrorOptions{})
	}
	if provider.Version == consumer.Version {
		reason := Coverage(provider.Description, consumer.Description)
		if reason == "" {
			return nil, nil
		}
		return nil, NewChordError(CodeContractMismatch, fmt.Sprintf("%s@%d does not cover the consumer's description (%s); forgotten version bump?", provider.Name, provider.Version, reason), ChordErrorOptions{})
	}
	adapter, ok := accepts[consumer.Version]
	if !ok || adapter == nil {
		return nil, NewChordError(CodeContractMismatch, fmt.Sprintf("%s: provider version %d, consumer version %d, no accepts adapter", provider.Name, provider.Version, consumer.Version), ChordErrorOptions{})
	}
	return adapter, nil
}

// Ref is contract.ts Ref<C> = RefBase<C> & MembersOf<C>: what a get returns for a contract (object.ts
// RefImpl locally, remote.ts RemoteRef over a link). Upstream's late-bound members are properties of a Proxy;
// Member reads one by name: a state member is a Readable[any], a method is a func(args any, ctx Ctx) (any, error)
// (ctx may be nil, as upstream's optional ctx), an opaque member is its native value, and nil is undefined.
type Ref interface {
	RefBase
	Member(name string) any
}

func (member MemberDescription) jsonValue() any {
	switch member.Kind {
	case MemberMethod:
		return map[string]any{"kind": member.Kind, "args": schemaValue(member.Args), "result": schemaValue(member.Result)}
	case MemberState:
		return map[string]any{"kind": member.Kind, "schema": schemaValue(member.Schema)}
	default:
		return map[string]any{"kind": member.Kind, "id": member.ID}
	}
}

// schemaValue is a schema as JSON data; a nil schema is undefined.
func schemaValue(schema Schema) any {
	if schema == nil {
		return absent{}
	}
	return schema
}

// jsonValue is the description as data for canonicalJson (which sorts keys, as Pi's does before hashing).
func (description Description) jsonValue() any {
	variants := map[string]any{}
	for _, variant := range description.Variants {
		out := map[string]any{}
		for _, member := range variant.Value {
			out[member.Name] = member.Value.jsonValue()
		}
		variants[variant.Name] = out
	}
	out := map[string]any{
		"name": description.Name, "scope": string(description.Scope), "version": float64(description.Version),
		"input": schemaValue(description.Input), "variants": variants,
	}
	if description.Host != nil {
		out["host"] = description.placementValue()
	}
	return out
}

// wireValue is the description as Pi's JSON.stringify writes it on a link (connection.ts:324 sends the description
// object): name, scope, version, input, then variants and members in their own-key order, each member as
// { kind, args, result }, { kind, schema } or { kind, id }; an undefined schema is left out. A schema's own keys are
// written sorted: Schema is a Go map.
func (description Description) wireValue() *chordjson.Object {
	out := chordjson.ObjectOf("name", description.Name, "scope", string(description.Scope), "version", float64(description.Version))
	setSchema(out, "input", description.Input)
	switch typed := description.Host.(type) {
	case HostNamed:
		out.Set("host", chordjson.ObjectOf("name", typed.Name))
	case HostFamily:
		out.Set("host", chordjson.ObjectOf("prefix", typed.Prefix, "key", typed.Key))
	}
	variants := chordjson.NewObject(len(description.Variants))
	for _, variant := range description.Variants {
		members := chordjson.NewObject(len(variant.Value))
		for _, member := range variant.Value {
			value := chordjson.ObjectOf("kind", member.Value.Kind)
			switch member.Value.Kind {
			case MemberMethod:
				setSchema(value, "args", member.Value.Args)
				setSchema(value, "result", member.Value.Result)
			case MemberState:
				setSchema(value, "schema", member.Value.Schema)
			default:
				value.Set("id", member.Value.ID)
			}
			members.Set(member.Name, value)
		}
		variants.Set(variant.Name, members)
	}
	out.Set("variants", variants)
	return out
}

// setSchema sets key to schema as JSON data; an undefined (nil) schema is left out, as JSON.stringify leaves it.
func setSchema(object *chordjson.Object, key string, schema Schema) {
	if schema == nil {
		return
	}
	value, err := chordjson.Decode([]byte(CanonicalJSON(schema)))
	if err != nil {
		panic(err)
	}
	object.Set(key, value)
}

// MarshalJSON writes the description's wire form (wireValue).
func (description Description) MarshalJSON() ([]byte, error) {
	return description.wireValue().MarshalJSON() //perf:ok once per contract description encoded (a register, a hold token); the wire form is an ordered chordjson object, so the Marshaler delegates to its encoder
}

// UnmarshalJSON reads a wire description, keeping the order of its variants and members.
func (description *Description) UnmarshalJSON(data []byte) error {
	var wire struct {
		Name    string `json:"name"`
		Scope   string `json:"scope"`
		Version int    `json:"version"`
		Input   Schema `json:"input"`
		Host    *struct {
			Name   *string `json:"name"`
			Prefix *string `json:"prefix"`
			Key    *string `json:"key"`
		} `json:"host"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	decoded, err := chordjson.Decode(data)
	if err != nil {
		return err
	}
	*description = Description{Name: wire.Name, Scope: ContractScope(wire.Scope), Version: wire.Version, Input: wire.Input}
	if wire.Host != nil {
		switch {
		case wire.Host.Name != nil:
			description.Host = HostNamed{Name: *wire.Host.Name}
		case wire.Host.Prefix != nil && wire.Host.Key != nil:
			description.Host = HostFamily{Prefix: *wire.Host.Prefix, Key: *wire.Host.Key}
		}
	}
	object, _ := decoded.(*chordjson.Object)
	if object == nil {
		return nil
	}
	variants, _ := object.Value("variants").(*chordjson.Object)
	if variants == nil {
		return nil
	}
	// The member values are read typed from their own text in data; the chordjson objects give only the order.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return err
	}
	var variantsRaw map[string]json.RawMessage
	if err := json.Unmarshal(top["variants"], &variantsRaw); err != nil {
		return err
	}
	for kind, value := range variants.All() {
		members, ok := value.(*chordjson.Object)
		if !ok {
			return fmt.Errorf("description variant %s is not an object", kind)
		}
		var membersRaw map[string]json.RawMessage
		if err := json.Unmarshal(variantsRaw[kind], &membersRaw); err != nil {
			return err
		}
		var out Fields[MemberDescription]
		for name := range members.All() {
			var member struct {
				Kind   string `json:"kind"`
				Args   Schema `json:"args"`
				Result Schema `json:"result"`
				Schema Schema `json:"schema"`
				ID     string `json:"id"`
			}
			if err := json.Unmarshal(membersRaw[name], &member); err != nil {
				return err
			}
			out = append(out, Field[MemberDescription]{Name: name, Value: MemberDescription{Kind: member.Kind, Args: member.Args, Result: member.Result, Schema: member.Schema, ID: member.ID}})
		}
		description.Variants = append(description.Variants, Field[Fields[MemberDescription]]{Name: kind, Value: out})
	}
	return nil
}
