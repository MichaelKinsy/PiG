package subprocess

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi keeps one effective registration root per provider: a partial re-registration merges over the previous root and keeps its oauth, and unregisterProvider removes the whole root (model-runtime.ts:753-797; provider-composer.ts adaptOAuth). The OAuth login a superseded registrant contributed therefore belongs to the latest registrant's cleanup.
func TestProviderOAuthFollowsLatestRegistrant(t *testing.T) {
	for _, exit := range []bool{false, true} {
		t.Run(map[bool]string{false: "unregister", true: "teardown"}[exit], func(t *testing.T) {
			const id = "oauth-ownership-provider"
			t.Cleanup(func() { ai.UnregisterOAuthProvider(id) })
			host := NewHost(t.TempDir())
			t.Cleanup(func() { host.Shutdown("test done") })
			host.SetProviderCallbacks(func(string, extension.ProviderConfig) error { return nil }, func(string) {})
			author := newOwnershipTestExt("author")
			later := newOwnershipTestExt("later")
			host.exts = map[string]*managedExt{"author": author, "later": later}
			// The unstarted Conns own no process; Shutdown must not wait for them.
			t.Cleanup(func() {
				host.mu.Lock()
				host.exts = map[string]*managedExt{}
				host.mu.Unlock()
			})
			register := func(me *managedExt, method, args string) {
				t.Helper()
				if _, err := host.handleProviderRegistrationCall(t.Context(), me, me.connection(), "", &CallPayload{Method: method, Args: json.RawMessage(args)}); err != nil {
					t.Fatalf("%s %s: %v", me.config.Name, method, err)
				}
			}
			register(author, "registerProvider", `{"name":"`+id+`","config":{"name":"Author","oauth":{"name":"Author OAuth","hasRefresh":true}}}`)
			register(later, "registerProvider", `{"name":"`+id+`","config":{"name":"Later"}}`)
			if _, ok := ai.GetOAuthProvider(id); !ok {
				t.Fatal("a partial re-registration dropped the merged root's OAuth")
			}
			if exit {
				host.unregisterOAuthProviders(author, nil)
				if _, ok := ai.GetOAuthProvider(id); !ok {
					t.Fatal("the superseded author's teardown removed the current registration's OAuth")
				}
				host.unregisterOAuthProviders(later, nil)
			} else {
				register(later, "unregisterProvider", `{"name":"`+id+`"}`)
			}
			if _, ok := ai.GetOAuthProvider(id); ok {
				t.Fatal("the provider's OAuth outlived the registration that carried it")
			}
		})
	}
}

// An unregister by an extension that never registered the provider still removes Pi's single registration and its oauth (model-runtime.ts:791-797), and the author's later teardown has nothing left to remove.
func TestForeignUnregisterRemovesProviderOAuth(t *testing.T) {
	const id = "oauth-foreign-unregister"
	t.Cleanup(func() { ai.UnregisterOAuthProvider(id) })
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	host.SetProviderCallbacks(func(string, extension.ProviderConfig) error { return nil }, func(string) {})
	author := newOwnershipTestExt("author")
	other := newOwnershipTestExt("other")
	host.exts = map[string]*managedExt{"author": author, "other": other}
	t.Cleanup(func() {
		host.mu.Lock()
		host.exts = map[string]*managedExt{}
		host.mu.Unlock()
	})
	if _, err := host.handleProviderRegistrationCall(t.Context(), author, author.connection(), "", &CallPayload{Method: "registerProvider", Args: json.RawMessage(`{"name":"` + id + `","config":{"name":"Author","oauth":{"name":"Author OAuth"}}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := host.handleProviderRegistrationCall(t.Context(), other, other.connection(), "", &CallPayload{Method: "unregisterProvider", Args: json.RawMessage(`{"name":"` + id + `"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := ai.GetOAuthProvider(id); ok {
		t.Fatal("the OAuth login outlived an unregister from another extension")
	}
	if got := host.ProviderNames("author"); len(got) != 0 {
		t.Fatalf("the author still claims the unregistered provider: %v", got)
	}
	var notified bool
	for len(author.connection().outCh) > 0 {
		frame := <-author.connection().outCh
		notified = notified || strings.Contains(string(frame.data), notifyProviderSuperseded)
	}
	if !notified {
		t.Fatal("the author process was not told that its root is no longer registered")
	}
}

// A later registration that carries its own oauth replaces the merged root's oauth (model-runtime.ts:753-766), even though the later registrant inherits the earlier login's cleanup claim.
func TestLaterProviderOAuthReplacesInheritedOAuth(t *testing.T) {
	const id = "oauth-later-replacement"
	t.Cleanup(func() { ai.UnregisterOAuthProvider(id) })
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	host.SetProviderCallbacks(func(string, extension.ProviderConfig) error { return nil }, func(string) {})
	author := newOwnershipTestExt("author")
	later := newOwnershipTestExt("later")
	host.exts = map[string]*managedExt{"author": author, "later": later}
	t.Cleanup(func() {
		host.mu.Lock()
		host.exts = map[string]*managedExt{}
		host.mu.Unlock()
	})
	for _, me := range []*managedExt{author, later} {
		if _, err := host.handleProviderRegistrationCall(t.Context(), me, me.connection(), "", &CallPayload{Method: "registerProvider", Args: json.RawMessage(`{"name":"` + id + `","config":{"name":"` + me.config.Name + `","oauth":{"name":"` + me.config.Name + ` OAuth"}}}`)}); err != nil {
			t.Fatal(err)
		}
	}
	provider, ok := ai.GetOAuthProvider(id)
	if proxy, isProxy := provider.(*oauthProxy); !ok || !isProxy || proxy.me != later || provider.Name() != "later OAuth" {
		t.Fatalf("OAuth provider = %#v, want the later registrant's", provider)
	}
	if got := host.OAuthProviderNames("later"); len(got) != 1 {
		t.Fatalf("later OAuth claims = %v, want one", got)
	}
}

// Pi's registerNativeProvider deletes the configuration registration, including its oauth (model-runtime.ts:744-751), so a native Provider without OAuth leaves no login behind.
func TestNativeProviderReplacesConfigurationOAuth(t *testing.T) {
	const id = "oauth-native-replacement"
	t.Cleanup(func() { ai.UnregisterOAuthProvider(id) })
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	host.SetProviderCallbacks(func(string, extension.ProviderConfig) error { return nil }, func(string) {})
	author := newOwnershipTestExt("author")
	native := newOwnershipTestExt("native")
	host.exts = map[string]*managedExt{"author": author, "native": native}
	t.Cleanup(func() {
		host.mu.Lock()
		host.exts = map[string]*managedExt{}
		host.mu.Unlock()
	})
	if _, err := host.handleProviderRegistrationCall(t.Context(), author, author.connection(), "", &CallPayload{Method: "registerProvider", Args: json.RawMessage(`{"name":"` + id + `","config":{"name":"Author","oauth":{"name":"Author OAuth"}}}`)}); err != nil {
		t.Fatal(err)
	}
	provider := &nativeProviderProxy{host: host, owner: native, conn: native.connection(), registered: true, references: map[*Conn]map[string]struct{}{}, declaration: NativeProviderDeclaration{ID: id, Key: "callback", Handle: "handle"}}
	host.nativeProviders = map[*Conn]map[string]*nativeProviderProxy{native.connection(): {id: provider}}
	host.publishNativeProvider(provider)
	if _, ok := ai.GetOAuthProvider(id); ok {
		t.Fatal("a native Provider without OAuth kept the replaced configuration's OAuth login")
	}
	if got := host.OAuthProviderNames("author"); len(got) != 0 {
		t.Fatalf("the replaced author still claims the OAuth login: %v", got)
	}
}

func newOwnershipTestExt(name string) *managedExt {
	return withConn(&managedExt{config: ExtConfig{Name: name}}, NewConn(name, nil))
}
