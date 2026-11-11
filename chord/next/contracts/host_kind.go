package contracts

import (
	"math"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// Ports packages/chord/src/contracts/host-kind.ts (host kinds and addresses (§3): a kind of addressable host with
// params like a contract's, and the address of one host of a kind, which a remote contract's `hostAddress` computes
// from its params).
//
// Forced Go differences. A host kind is a callable function upstream; HostKind is a struct and Address(values) is the
// call. The WeakMap of kind params is the kind itself (its data is collected with it). `hostKind` is NewHostKind. The
// type-level KindShape, Address<K>, ParamsOfKind and HostKind<N, P> parameters are designed out; an address's params
// are a *delta.JsonObject.

// Address is one host of a kind: the kind's name, its params and its key (`session|S1`); two addresses are equal by
// key. Params are what the caller passed, unchecked until the address is routed.
type Address struct {
	Kind   string
	Params *delta.JsonObject
	Key    string
}

// HostKind is a kind of addressable host: given its params, it makes an address. Always held by pointer; a value not
// made by NewHostKind is refused by KindParams.
type HostKind struct {
	Kind string

	params *ParamsData
	// fields are the params fields as address keys hold them (read once, at declaration).
	fields []keyField
}

// keyField is one params field as an address key holds it: its name, its type and, for `oneOf(...)`, its literals.
type keyField struct {
	name     string
	typeName string
	literals map[string]struct{}
}

// NewHostKind declares a host kind (upstream's `hostKind`); params follow the params policy of contracts (a flat record
// of required Str(), Int(), Bool() and OneOf(...)). Making an address computes its key only: its params are checked
// where it is routed.
func NewHostKind(name string, params ...Property) (*HostKind, error) {
	if err := CheckName(name, "A host kind's name"); err != nil {
		return nil, err
	}
	// `client` is the host kind of a host without an address in store names (owner strings, table names).
	if name == "client" {
		return nil, fail(`A host kind's name "client" is reserved for hosts without an address`)
	}
	data, err := ReadParams(params, name+".params")
	if err != nil {
		return nil, err
	}
	return &HostKind{Kind: name, params: data, fields: fieldsOf(data)}, nil
}

// Address is the address of the host of this kind with the given params (nil is `{}`).
func (kind *HostKind) Address(values *delta.JsonObject) Address {
	if values == nil {
		values = delta.NewJsonObject(0)
	}
	return Address{Kind: kind.Kind, Params: values, Key: AddressKey(kind.Kind, kind.params, values)}
}

// KindParams is the params of a declared host kind.
func KindParams(kind *HostKind) (*ParamsData, error) {
	if kind == nil || kind.params == nil {
		name := "undefined"
		if kind != nil {
			name = kind.Kind
		}
		return nil, fail("%s: not a declared host kind", name)
	}
	return kind.params, nil
}

// AddressKey is an address's key: the kind's name and each field value in declared order, joined (JoinKey). A value is
// converted as JavaScript's String does; an absent field is "undefined".
func AddressKey(kind string, params *ParamsData, values *delta.JsonObject) string {
	parts := make([]string, 0, len(params.Fields)+1)
	parts = append(parts, kind)
	for _, field := range params.Fields {
		value, present := values.Get(field)
		if !present {
			parts = append(parts, "undefined")
			continue
		}
		parts = append(parts, jsString(value))
	}
	return JoinKey(parts)
}

// jsString is String(value) for a JSON value.
func jsString(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return jsnumber.String(typed)
	case []any:
		items := make([]string, len(typed))
		for i, item := range typed {
			if item != nil {
				items[i] = jsString(item)
			}
		}
		return strings.Join(items, ",")
	case *delta.JsonObject:
		return "[object Object]"
	}
	return "undefined"
}

// fieldsOf is the fields of declared params as keys hold them (ReadParams admitted only these normal forms).
func fieldsOf(data *ParamsData) []keyField {
	// Normal is `[field, normal form]` pairs in identity order.
	pairs := data.Normal.([]any)
	fields := make([]keyField, len(pairs))
	for i, pair := range pairs {
		entry := pair.([]any)
		node := entry[1].(*delta.JsonObject)
		field := keyField{name: entry[0].(string)}
		if branches, present := node.Get("anyOf"); present {
			// `oneOf(...)` of string literals: `{ type: "string", const }` branches.
			field.typeName = "string"
			field.literals = map[string]struct{}{}
			for _, branch := range branches.([]any) {
				field.literals[branch.(*delta.JsonObject).Value("const").(string)] = struct{}{}
			}
		} else {
			field.typeName, _ = node.Value("type").(string)
		}
		fields[i] = field
	}
	return fields
}

// KeyOfAddress is the key of value when it is an address of kind (a trust boundary for values a plugin hands in: a
// host-keyed document's key, a `host(...)` cell): an Address of that kind whose params hold exactly the kind's fields,
// each of its type (a `oneOf(...)` one among its literals; other constraints are checked where the address is routed).
// The key is computed from the params, never taken from the value's Key. ok is false when it does not fit.
func KeyOfAddress(kind *HostKind, value any) (string, bool) {
	var address Address
	switch typed := value.(type) {
	case Address:
		address = typed
	case *Address:
		if typed == nil {
			return "", false
		}
		address = *typed
	default:
		return "", false
	}
	// Declaration policy: callers checked the kind (KindParams) when they took it; an undeclared one fits nothing.
	if kind == nil || kind.params == nil || address.Kind != kind.Kind || address.Params == nil || address.Params.Len() != len(kind.fields) {
		return "", false
	}
	for _, field := range kind.fields {
		each, present := address.Params.Get(field.name)
		if !present {
			return "", false
		}
		fits := false
		switch field.typeName {
		case "integer":
			number, isNumber := each.(float64)
			fits = isNumber && number == math.Trunc(number) && math.Abs(number) <= 1<<53-1
		case "boolean":
			_, fits = each.(bool)
		default:
			text, isText := each.(string)
			if isText {
				_, listed := field.literals[text]
				fits = field.literals == nil || listed
			}
		}
		if !fits {
			return "", false
		}
	}
	return AddressKey(kind.Kind, kind.params, address.Params), true
}

// AddressOfKey is the address of kind whose key is key (a key KeyOfAddress made: stored data): the escaped parts split,
// `int()` fields as numbers, `bool()` fields as booleans. A key of another kind or number of parts is an error.
func AddressOfKey(kind *HostKind, key string) (Address, error) {
	if _, err := KindParams(kind); err != nil {
		return Address{}, err
	}
	parts := splitKey(key)
	if parts[0] != kind.Kind || len(parts)-1 != len(kind.fields) {
		return Address{}, fail("%s: not an address key of %s", quote(key), kind.Kind)
	}
	params := delta.NewJsonObject(len(kind.fields))
	for i, field := range kind.fields {
		part := parts[i+1]
		switch field.typeName {
		case "integer":
			number, err := strconv.ParseFloat(part, 64)
			if err != nil {
				number = math.NaN()
			}
			params.Set(field.name, number)
		case "boolean":
			params.Set(field.name, part == "true")
		default:
			params.Set(field.name, part)
		}
	}
	return Address{Kind: kind.Kind, Params: params, Key: key}, nil
}

// KindOfKey is the kind's name of an address key: its first part (kind names never hold `|` or a backslash).
func KindOfKey(key string) string {
	if before, _, ok := strings.Cut(key, "|"); ok {
		return before
	}
	return key
}

// splitKey is the parts of a key JoinKey made, unescaped.
func splitKey(key string) []string {
	var parts []string
	var part strings.Builder
	for i := 0; i < len(key); i++ {
		switch char := key[i]; char {
		case '\\':
			i++
			if i < len(key) {
				part.WriteByte(key[i])
			}
		case '|':
			parts = append(parts, part.String())
			part.Reset()
		default:
			part.WriteByte(char)
		}
	}
	return append(parts, part.String())
}
