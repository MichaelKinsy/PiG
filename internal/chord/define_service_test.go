package chord

import "testing"

type definedServiceProbe interface{ Ping() }

func panicMessage(t *testing.T, f func()) (message any) {
	t.Helper()
	defer func() { message = recover() }()
	f()
	return nil
}

// Pi packages/chord/src/api.ts:73-85 defineService: the three declared overloads (options { local: true } required, options
// { local?: false } optional, and the implementation's optional options) differ only in which options they accept; Go spells an
// optional parameter as one variadic. A service declared without options is remote, `{ local: true }` makes it process-local,
// an explicit `{ local: false }` stays remote, the id is kept as given, an empty id throws TypeError("Service ID must not be
// empty"), and an id under the reserved `$chord.` namespace throws.
func TestDefineServiceAcceptsEachOverloadOfUpstream(t *testing.T) {
	for _, tc := range []struct {
		name      string
		options   []ServiceOptions
		wantLocal bool
	}{
		{"no options", nil, false},
		{"local true", []ServiceOptions{{Local: true}}, true},
		{"local false", []ServiceOptions{{Local: false}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definition := DefineService[definedServiceProbe]("test.define."+tc.name, tc.options...)
			if definition.Id() != "test.define."+tc.name || definition.Local() != tc.wantLocal {
				t.Fatalf("definition = {id %q local %v}, want {id %q local %v}", definition.Id(), definition.Local(), "test.define."+tc.name, tc.wantLocal)
			}
		})
	}
	if got := panicMessage(t, func() { DefineService[definedServiceProbe]("") }); got != "Service ID must not be empty" {
		t.Fatalf("empty id panic = %v, want upstream's TypeError message", got)
	}
	if got := panicMessage(t, func() { DefineService[definedServiceProbe]("$chord.internal") }); got != "Service IDs beginning with $chord. are reserved" {
		t.Fatalf("reserved id panic = %v", got)
	}
	if got := panicMessage(t, func() { DefineService[definedServiceProbe]("chord.public") }); got != nil {
		t.Fatalf("an id that only contains chord. panicked: %v", got)
	}
}
