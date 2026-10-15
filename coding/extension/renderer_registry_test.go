package extension

import (
	"sync"
	"testing"
)

// A renderer registered while other goroutines look renderers up is race-free and visible to the next lookup, replacing an earlier one.
func TestRendererRegistrationAfterLoadIsSynchronized(t *testing.T) {
	ext := &Extension{}
	ext.InitializeDeclarationRegistry()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				_, _ = ext.MessageRendererFor("t")
				_, _ = ext.EntryRendererFor("t")
			}
		})
	}
	first := MessageRenderer(nil)
	ext.SetMessageRenderer("t", first)
	ext.SetEntryRenderer("t", nil)
	wg.Wait()
	if _, ok := ext.MessageRendererFor("t"); !ok {
		t.Fatal("message renderer registered after load is not found")
	}
	if _, ok := ext.EntryRendererFor("t"); !ok {
		t.Fatal("entry renderer registered after load is not found")
	}
}
