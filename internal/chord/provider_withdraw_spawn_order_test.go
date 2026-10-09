package chord

import (
	"context"
	"errors"
	"testing"
)

// Pi packages/chord/src/services/provider.ts:129-140 (withdraw), 172-181 (use) and 183-212 (spawn): each runs
// #assertActive, #assertRemotable and #assertAllowed first; withdraw and spawn then check the registration mode, and
// spawn checks the key before the mode and the implementation after it. Each expected code and message is what Pi's
// provider reports for the same calls under Node.
func TestProviderWithdrawUseSpawnChecksMatchPi(t *testing.T) {
	a, k, other := defineReader("pws.a"), defineReader("pws.k"), defineReader("pws.other")
	local := DefineService[Reader]("pws.local", ServiceOptions{Local: true})
	valid := Reader(readerFunc(func(context.Context) (string, error) { return "", nil }))
	var bad Reader
	newProvider := func() *RemoteServiceProvider {
		provider, err := NewRemoteServiceProvider(ServiceProviderDefinition{Id: a.Id()}, ServiceProviderDefinition{Id: k.Id(), Mode: ServiceKeyed})
		if err != nil {
			t.Fatal(err)
		}
		return provider
	}
	describe := func(err error) string {
		if err == nil {
			return "ok"
		}
		if remote, ok := errors.AsType[*RemoteServiceError](err); ok {
			return string(remote.Code) + " " + remote.Message
		}
		return "- " + err.Error()
	}
	use := func(p *RemoteServiceProvider, def ServiceDefinition[Reader]) error { _, err := Use(p, def); return err }
	spawn := func(p *RemoteServiceProvider, def ServiceDefinition[Reader], key string, impl Reader) error {
		_, err := Spawn(p, def, key, impl)
		return err
	}
	type step struct {
		label string
		run   func(*RemoteServiceProvider) error
		want  string
	}
	groups := [][]step{
		{
			{"dispose", func(p *RemoteServiceProvider) error { return p.Dispose() }, "ok"},
			{"withdraw disposed", func(p *RemoteServiceProvider) error { return Withdraw(p, a) }, "- Remote service provider is disposed"},
			{"use disposed", func(p *RemoteServiceProvider) error { return use(p, a) }, "- Remote service provider is disposed"},
			{"spawn disposed+emptykey", func(p *RemoteServiceProvider) error { return spawn(p, k, "", valid) }, "- Remote service provider is disposed"},
			{"withdraw disposed local", func(p *RemoteServiceProvider) error { return Withdraw(p, local) }, "- Remote service provider is disposed"},
		},
		{
			{"withdraw local", func(p *RemoteServiceProvider) error { return Withdraw(p, local) }, "service_not_allowed Service pws.local is process-local"},
			{"withdraw notallowed", func(p *RemoteServiceProvider) error { return Withdraw(p, other) }, "service_not_allowed Remote service pws.other is not allowlisted"},
			{"withdraw keyed", func(p *RemoteServiceProvider) error { return Withdraw(p, k) }, "service_mode_mismatch Remote service pws.k is keyed, not singleton"},
			{"withdraw none", func(p *RemoteServiceProvider) error { return Withdraw(p, a) }, "ok"},
			{"use local", func(p *RemoteServiceProvider) error { return use(p, local) }, "service_not_allowed Service pws.local is process-local"},
			{"use notallowed", func(p *RemoteServiceProvider) error { return use(p, other) }, "service_not_allowed Remote service pws.other is not allowlisted"},
			{"use keyed", func(p *RemoteServiceProvider) error { return use(p, k) }, "service_not_found Remote service pws.k has no local provider"},
			{"use none", func(p *RemoteServiceProvider) error { return use(p, a) }, "service_not_found Remote service pws.a has no local provider"},
		},
		{
			{"spawn local", func(p *RemoteServiceProvider) error { return spawn(p, local, "", bad) }, "service_not_allowed Service pws.local is process-local"},
			{"spawn notallowed", func(p *RemoteServiceProvider) error { return spawn(p, other, "", bad) }, "service_not_allowed Remote service pws.other is not allowlisted"},
			{"spawn emptykey+singleton", func(p *RemoteServiceProvider) error { return spawn(p, a, "", bad) }, "- Remote service instance key must not be empty"},
			{"spawn singleton", func(p *RemoteServiceProvider) error { return spawn(p, a, "x", bad) }, "service_mode_mismatch Remote service pws.a is singleton, not keyed"},
			{"spawn bad impl", func(p *RemoteServiceProvider) error { return spawn(p, k, "x", bad) }, "- Remote service pws.k implementation must be an object"},
			{"spawn ok", func(p *RemoteServiceProvider) error { return spawn(p, k, "x", valid) }, "ok"},
			{"spawn dup+bad", func(p *RemoteServiceProvider) error { return spawn(p, k, "x", bad) }, "service_mode_mismatch Remote service pws.k already has a live instance with key x"},
		},
		{
			{"spawn close close respawn", func(p *RemoteServiceProvider) error {
				closeInstance, err := Spawn(p, k, "x", valid)
				if err != nil {
					return err
				}
				if err := errors.Join(closeInstance(), closeInstance()); err != nil {
					return err
				}
				return spawn(p, k, "x", valid)
			}, "ok"},
			{"provide withdraw withdraw", func(p *RemoteServiceProvider) error {
				return errors.Join(Provide(p, a, valid), Withdraw(p, a), Withdraw(p, a))
			}, "ok"},
			{"use after withdraw", func(p *RemoteServiceProvider) error { return use(p, a) }, "service_not_found Remote service pws.a has no local provider"},
			{"provide after withdraw", func(p *RemoteServiceProvider) error { return Provide(p, a, valid) }, "ok"},
			{"use", func(p *RemoteServiceProvider) error { return use(p, a) }, "ok"},
		},
	}
	for _, group := range groups {
		provider := newProvider()
		for _, s := range group {
			if got := describe(s.run(provider)); got != s.want {
				t.Errorf("%s: got %q, want %q", s.label, got, s.want)
			}
		}
	}
}
