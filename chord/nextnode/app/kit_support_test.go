package app

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// kitHost is a host on the store in dir/store.sqlite with facets loaded: the Go form of the app kit's server host as the kit's
// own tests need it (the app, startServer, is composed on top of such a host). closeKit closes the host and the store.
type kitHost struct {
	t      *testing.T
	store  *store.ChordStore
	host   host.Host
	errors *errorLog
}

func newKitHost(t *testing.T, dir string, options host.HostOptions, facets ...host.HostFacet) *kitHost {
	t.Helper()
	chordStore := openStoreAt(t, filepath.Join(dir, "store.sqlite"))
	errs := &errorLog{}
	options.Store = chordStore
	options.OnError = errs.push
	h, err := host.CreateHost(options)
	if err != nil {
		t.Fatal(err)
	}
	report, err := h.Load(facets, background)
	if err != nil {
		t.Fatal(err)
	}
	for _, failed := range report.Failed {
		t.Fatalf("plugin %q was left out: %v", failed.Plugin, failed.Error)
	}
	return &kitHost{t: t, store: chordStore, host: h, errors: errs}
}

func (k *kitHost) close() {
	k.t.Helper()
	if err := k.host.Close(background); err != nil {
		k.t.Fatalf("host close: %v", err)
	}
	ctx, cancel := context.WithTimeout(background, 10*time.Second)
	defer cancel()
	if err := k.store.Close(ctx); err != nil {
		k.t.Fatalf("store close: %v", err)
	}
}

func stringsReader(text string) io.Reader { return strings.NewReader(text) }
