package chord

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// upstream: packages/chord/src/services/consumer.ts validateResetSnapshot. The consumer checks a reset itself, because an in-process transport delivers updates without the wire parser's check.
func TestConsumerRejectsMalformedResets(t *testing.T) {
	state := func(ops ...Op) ServiceMemberSnapshot {
		return ServiceMemberSnapshot{Name: "state", Kind: MemberState, Ops: ops}
	}
	root := state(Op{"r", chordjson.NewObject(0)})
	key := func(name string) *ServiceInstanceAddress { return &ServiceInstanceAddress{Key: name, Generation: 1} }
	for _, tc := range []struct {
		name     string
		snapshot ServiceSubscriptionSnapshot
		mode     ServiceMode
		want     string
	}{
		{"another service", ServiceSubscriptionSnapshot{ServiceId: "other", Mode: ServiceSingleton}, ServiceSingleton, "Remote service reset has the wrong service or mode"},
		{"another mode", ServiceSubscriptionSnapshot{ServiceId: "svc", Mode: ServiceKeyed}, ServiceSingleton, "Remote service reset has the wrong service or mode"},
		{"two singletons", ServiceSubscriptionSnapshot{ServiceId: "svc", Mode: ServiceSingleton, Instances: []ServiceInstanceSnapshot{{}, {}}}, ServiceSingleton, "Remote service reset has the wrong service or mode"},
		{"addressed singleton", ServiceSubscriptionSnapshot{ServiceId: "svc", Mode: ServiceSingleton, Instances: []ServiceInstanceSnapshot{{Instance: key("a")}}}, ServiceSingleton, "Remote service reset has an invalid instance address"},
		{"unaddressed keyed instance", ServiceSubscriptionSnapshot{ServiceId: "svc", Mode: ServiceKeyed, Instances: []ServiceInstanceSnapshot{{}}}, ServiceKeyed, "Remote service reset has an invalid instance address"},
		{"repeated key", ServiceSubscriptionSnapshot{ServiceId: "svc", Mode: ServiceKeyed, Instances: []ServiceInstanceSnapshot{{Instance: key("a")}, {Instance: key("a")}}}, ServiceKeyed, "Remote service reset repeats an instance key"},
		{"delta state", ServiceSubscriptionSnapshot{ServiceId: "svc", Mode: ServiceSingleton, Instances: []ServiceInstanceSnapshot{{Members: []ServiceMemberSnapshot{state(Op{"s", []any{"a"}, 1.0})}}}}, ServiceSingleton, "Remote service reset must contain full root replacements"},
		{"two state ops", ServiceSubscriptionSnapshot{ServiceId: "svc", Mode: ServiceKeyed, Instances: []ServiceInstanceSnapshot{{Instance: key("a"), Members: []ServiceMemberSnapshot{state(Op{"r", chordjson.NewObject(0)}, Op{"s", []any{"a"}, 1.0})}}}}, ServiceKeyed, "Remote service reset must contain full root replacements"},
		{"empty singleton", ServiceSubscriptionSnapshot{ServiceId: "svc", Mode: ServiceSingleton}, ServiceSingleton, ""},
		{"root replacements", ServiceSubscriptionSnapshot{ServiceId: "svc", Mode: ServiceKeyed, Instances: []ServiceInstanceSnapshot{{Instance: key("a"), Members: []ServiceMemberSnapshot{root, {Name: "call", Kind: MemberMethod}}}, {Instance: key("b")}}}, ServiceKeyed, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateResetSnapshot(tc.snapshot, "svc", tc.mode)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
