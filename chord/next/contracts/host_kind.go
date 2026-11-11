package contracts

import (
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// Ports packages/chord/src/next/contracts/host-kind.ts (host kinds and addresses (§3): a kind of addressable host with
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
	return &HostKind{Kind: name, params: data}, nil
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
