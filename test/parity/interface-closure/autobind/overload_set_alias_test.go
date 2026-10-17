package main

import (
	"go/types"
	"testing"
)

// TestOverloadSetAlias pins A10: a `UnionToIntersection<...>` overload set (telemetry bindTypedSpanStarter) agrees with a Go function
// whose first parameter is the span name string and whose last is the callback, and with nothing else.
func TestOverloadSetAlias(t *testing.T) {
	str := types.Typ[types.String]
	anyT := types.NewInterfaceType(nil, nil)
	callback := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	fn := func(params ...types.Type) types.Type {
		vars := make([]*types.Var, len(params))
		for i, p := range params {
			vars[i] = types.NewParam(0, nil, "", p)
		}
		return types.NewSignatureType(nil, nil, nil, types.NewTuple(vars...), nil, false)
	}
	body := "UnionToIntersection<{ [Name in Names]: Starter<Name> }[Names]>"
	if _, ok := overloadSetAlias("Record<string, number>", fn(str, anyT, callback)); ok {
		t.Fatal("a body that is not UnionToIntersection<...> is not A10's")
	}
	for name, tc := range map[string]struct {
		typ  types.Type
		want tri
	}{
		"name, attributes, callback": {fn(str, anyT, callback), yes},
		"not a function":             {str, no},
		"too few parameters":         {fn(str, callback), no},
		"first is not a string":      {fn(anyT, anyT, callback), no},
		"last is not a callback":     {fn(str, anyT, anyT), no},
	} {
		v, ok := overloadSetAlias(body, tc.typ)
		if !ok || v.ok != tc.want {
			t.Errorf("%s: verdict %+v (applied %v), want %v", name, v, ok, tc.want)
		}
	}
}
