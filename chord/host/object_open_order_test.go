package host

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// object.ts:905-923 and 137-155: a batch's opens start in order and each resource joins openOrder when its open
// completes, so an earlier resource whose open really waits objects after a later one that finished first, and the
// object closes them in reverse completion order.
func TestResourcesCloseInReverseCompletionOrder(t *testing.T) {
	host := quiet(t, nil)
	log := &stringLog{}
	release := make(chan struct{})
	var fast Resource[any]
	typ := emptyLocal("t.completion-order")
	mustServe(t, host, typ, func(_ any, _ Principal, self Self) (Implementation, error) {
		if _, err := self.Resource("slow", ResourceDeclaration[any]{
			Version: 1,
			Open: func(ResourceOpenOptions) (any, error) {
				<-release
				return "slow", nil
			},
			Close: func(any) error { log.add("close slow"); return nil },
		}); err != nil {
			return nil, err
		}
		var err error
		fast, err = self.Resource("fast", ResourceDeclaration[any]{
			Version: 1,
			Open:    func(ResourceOpenOptions) (any, error) { return "fast", nil },
			Close:   func(any) error { log.add("close fast"); return nil },
		})
		return chordjson.ObjectOf(), err
	}, ServeOptions{})
	ref := mustRef(t, host, typ, map[string]any{}, nil)
	waitFor(t, "fast opened", func() bool {
		var opened bool
		host.run(func() {
			if fast != nil {
				opened = resourceOf(fast).state == "open"
			}
		})
		return opened
	})
	close(release)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	ref.Release()
	waitFor(t, "both closed", func() bool { return len(log.list()) == 2 })
	if got := log.list(); !slices.Equal(got, []string{"close slow", "close fast"}) {
		t.Fatalf("close order %v, want the reverse of completion order", got)
	}
}
