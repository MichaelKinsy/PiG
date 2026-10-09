package chord

import "testing"

// Pi packages/chord/src/services/provider.ts:88-94: the RemoteServiceProvider constructor checks every entry for a local
// service (:88-92) before it checks the IDs for duplicates (:93-94). A local entry is therefore the error even after a
// duplicate pair. The messages below are what Pi's constructor throws for these entries under Node.
func TestNewRemoteServiceProviderReportsALocalServiceBeforeADuplicateID(t *testing.T) {
	for _, c := range []struct {
		name    string
		entries []ServiceProviderDefinition
		want    string
	}{
		{"duplicate then local", []ServiceProviderDefinition{{Id: "a"}, {Id: "a"}, {Id: "b", Local: true}}, "Local service b cannot be published remotely"},
		{"duplicate only", []ServiceProviderDefinition{{Id: "a"}, {Id: "a"}}, "Remote service catalogue contains duplicate IDs"},
		{"local duplicated", []ServiceProviderDefinition{{Id: "c", Local: true, Mode: ServiceKeyed}, {Id: "c"}}, "Local service c cannot be published remotely"},
	} {
		provider, err := NewRemoteServiceProvider(c.entries...)
		if err == nil || err.Error() != c.want || provider != nil {
			t.Errorf("%s: NewRemoteServiceProvider = %v, %v; want nil, %q", c.name, provider, err, c.want)
		}
	}
}
