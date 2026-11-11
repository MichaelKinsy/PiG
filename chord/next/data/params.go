package data

import (
	"context"
	"strconv"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// Ports packages/chord/src/next/data/params.ts: the checked params of a get, their key, and the in-process identity of
// an object.

// CheckedParams is the result of CheckParams: the caller's own params object, and its key.
type CheckedParams struct {
	Params any
	Key    string
}

// CheckParams checks raw against the contract's params with one compiled closed-object check (`invalid/params` on
// failure) and returns the caller's own object with its key: the field values in declared order, each escaped, joined
// with `|`. raw is a *delta.JsonObject or a map[string]any.
func CheckParams(contract *contracts.Contract, raw any, ctx context.Context) (CheckedParams, error) {
	contractData, err := contracts.ContractDataOf(contract)
	if err != nil {
		return CheckedParams{}, err
	}
	params := contractData.Params
	validator, err := ValidatorFor(params, ctx)
	if err != nil {
		return CheckedParams{}, err
	}
	if err := Check(validator, raw, ReasonParams, ctx); err != nil {
		return CheckedParams{}, err
	}
	// The check above establishes the params type: a flat record of the declared scalar fields.
	parts := make([]string, len(params.Fields))
	for index, field := range params.Fields {
		parts[index] = fieldText(member(raw, field))
	}
	return CheckedParams{Params: raw, Key: contracts.JoinKey(parts)}, nil
}

// IdentityKey is the in-process identity of an object: `name|key|principalKey`, each part escaped; a `local`
// contract has no principal, so its call passes none (upstream's type refuses one; Go checks nothing, and a
// principal key passed for a local contract becomes a part of the identity).
func IdentityKey(contract *contracts.Contract, key string, principalKey ...string) string {
	parts := make([]string, 0, 2+len(principalKey))
	parts = append(parts, contract.Name, key)
	parts = append(parts, principalKey...)
	return contracts.JoinKey(parts)
}

// CheckAddress is an address from a kind's name and params as a hold names it or a `hostAddress` returned it: the
// params checked against the kind's with one compiled closed-object check (`invalid/host_address` on failure), the
// key computed. raw is a *delta.JsonObject or a map[string]any.
func CheckAddress(kind string, params *contracts.ParamsData, raw any, ctx context.Context) (contracts.Address, error) {
	validator, err := ValidatorFor(params, ctx)
	if err != nil {
		return contracts.Address{}, err
	}
	if err := Check(validator, raw, ReasonHostAddress, ctx); err != nil {
		return contracts.Address{}, err
	}
	// The check above establishes a flat record of the kind's scalar fields.
	values, _ := raw.(*delta.JsonObject)
	if values == nil {
		values = delta.NewJsonObject(len(params.Fields))
		for _, field := range params.Fields {
			values.Set(field, member(raw, field))
		}
	}
	return contracts.Address{Kind: kind, Params: values, Key: contracts.AddressKey(kind, params, values)}, nil
}

// member is the value of a field of a checked params record.
func member(record any, field string) any {
	switch typed := record.(type) {
	case *delta.JsonObject:
		return typed.Value(field)
	case map[string]any:
		return typed[field]
	}
	return nil
}

// fieldText is JavaScript's String(value) of a scalar field value.
func fieldText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return jsnumber.String(typed)
	case int:
		return jsnumber.String(float64(typed))
	}
	return ""
}
