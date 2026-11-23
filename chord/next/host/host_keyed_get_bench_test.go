package host

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

func warmGoroutineID() {
	for goroutineid.Proving() {
		done := make(chan struct{})
		go func() {
			_ = goroutineid.Current()
			close(done)
		}()
		<-done
	}
}

func keyedForBench(b *testing.B) (*documents.Keyed, *Reference, Host) {
	b.Helper()
	warmGoroutineID()
	var mu sync.Mutex
	var notes *documents.Keyed
	def, err := documents.DefineState("bench.notes", documents.StateOptions{
		Schema:  refNote,
		Version: 1,
		Initial: func() *documents.JsonObject { return refTitle("") },
		Keyed:   true,
	})
	if err != nil {
		b.Fatal(err)
	}
	host, _ := loaded(b, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(refNoteStore, refStoreFactory(def, func(h *documents.Keyed) {
			mu.Lock()
			notes = h
			mu.Unlock()
		}), ServeOptions{})
	}, loadedOptions{})
	ref := mustGet(b, host, refNoteStore, jsonObject())
	b.Cleanup(func() {
		ref.Release()
		_ = host.Close(context.Background())
	})
	refWrite(b, ref, "k", "v")
	mu.Lock()
	defer mu.Unlock()
	return notes, ref, host
}

// BenchmarkKeyedGetOffLoop is a cached keyed handle Get from outside the host loop (one section).
func BenchmarkKeyedGetOffLoop(b *testing.B) {
	handle, _, _ := keyedForBench(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := handle.Get(background, "k"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkKeyedGetInSection is a cached keyed handle Get from inside a section of the host loop.
func BenchmarkKeyedGetInSection(b *testing.B) {
	handle, _, host := keyedForBench(b)
	b.ReportAllocs()
	host.(*chordHost).enter(func() {
		for b.Loop() {
			if _, err := handle.Get(background, "k"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkReferenceKeyedGetOffLoop is a consumer ReferenceKeyed.Get off the host loop (covers ownAccess).
func BenchmarkReferenceKeyedGetOffLoop(b *testing.B) {
	_, ref, _ := keyedForBench(b)
	member := ref.Keyed("notes")
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := member.Get(background, "k"); err != nil {
			b.Fatal(err)
		}
	}
}
