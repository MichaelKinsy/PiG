package extensionconformance

import (
	"strings"
	"testing"
)

// notifySubscriptions are extension-facing subscriptions driven by a host
// notify rather than a host call. TestEverySDKCanReachEveryWireCapability
// cannot see them: it derives its denominator from the call dispatcher, and
// these make no call. Without a gate of their own, one SDK can silently lack a
// subscription every other SDK has.
//
// The value is the per-SDK symbol that must exist, so an exemption would have
// to name a mechanism rather than grant a blanket difference.
var notifySubscriptions = map[string]map[string]string{
	// Upstream Pi installs headers and footers as component factories whose
	// render(width) runs every frame, so they follow a resize with no work from
	// the extension. A pig extension is a subprocess and sends static lines, so
	// a footer keeps the width it was built for until something re-pushes it.
	// Every SDK must therefore offer the resize trigger.
	"width_change": {
		"go":   "func (c Context) OnWidthChange(",
		"rust": "pub fn on_width_change<F>(",
		"py":   "def on_width_change(",
		"node": "onWidthChange(handler)",
	},
}

func TestEverySDKExposesEveryNotifySubscription(t *testing.T) {
	root := repoRoot(t)
	sources := sdkSources(t, root)

	for notify, perSDK := range notifySubscriptions {
		for _, sdk := range sdkNames() {
			symbol, declared := perSDK[sdk]
			if !declared {
				t.Errorf("%s: no symbol declared for the %s SDK; every SDK must expose it "+
					"or record why it cannot", notify, sdk)
				continue
			}
			if !strings.Contains(sources[sdk], symbol) {
				t.Errorf("%s: the %s SDK does not expose %q. A capability in one SDK is "+
					"unfinished until it lands in all of them, or an extension is not "+
					"portable between them by rewriting.", notify, sdk, symbol)
			}
		}
	}
}

// The host must actually emit the notify each subscription is named for;
// otherwise every SDK exposes a subscription that can never fire.
func TestNotifySubscriptionsAreEmittedByTheHost(t *testing.T) {
	root := repoRoot(t)
	host := readRepoFile(t, root, "coding", "extension", "host", "subprocess", "host.go")

	for notify := range notifySubscriptions {
		if !strings.Contains(host, `"`+notify+`"`) {
			t.Errorf("no host code emits the %q notify, so the subscription every SDK "+
				"exposes for it can never fire", notify)
		}
	}
}
