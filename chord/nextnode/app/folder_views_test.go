package app

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/test/node/app/folder-views.test.ts: live folder listings on their own (decision "Files v2"): a
// view attaches with the listing read now, a change of its space reads each watched folder once and tells its
// observers, a change during an attach makes it read again, and a detached view is never read or told again.

// listing names n, for telling reads apart.
func listing(n int) FolderListing {
	return FolderListing{Entries: []FolderEntry{{Kind: "folder", Folder: &FolderInfo{Space: "s", Path: "f" + string(rune('0'+n)), Created: int64(n)}}}}
}

// recorder is an observer recording the values it is told.
type recorder struct {
	mu   sync.Mutex
	told []delta.JsonValue
}

func (r *recorder) Advance(call chord.WatchCall[any], _ context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if call.Kind == chord.WatchCallValue {
		r.told = append(r.told, call.Value)
	} else {
		r.told = append(r.told, call)
	}
}

func (r *recorder) End(chord.WatchEnd) {}

func (r *recorder) all() []delta.JsonValue {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]delta.JsonValue{}, r.told...)
}

func TestFolderViewsAttachWithTheListingNowAChangeOfItsSpaceReadsEachWatchedFolderOnce(t *testing.T) {
	views := NewFolderViews()
	var reads []string
	views.Bind(func(space, folder string, _ context.Context) (FolderListing, error) {
		reads = append(reads, space+":"+folder)
		return listing(len(reads)), nil
	})
	a, b, other := &recorder{}, &recorder{}, &recorder{}
	first, err := views.Attach("s", "docs", a, background)
	if err != nil || !reflect.DeepEqual(first.Value, listing(1)) {
		t.Fatalf("first attach = %+v, %v", first, err)
	}
	if _, err := views.Attach("s", "docs", b, background); err != nil {
		t.Fatal(err)
	}
	if _, err := views.Attach("t", "docs", other, background); err != nil {
		t.Fatal(err)
	}
	reads = nil
	if err := views.Changed("s", background); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reads, []string{"s:docs"}) {
		t.Fatalf("reads = %v", reads)
	}
	want := []delta.JsonValue{listing(1).JSON()}
	if !reflect.DeepEqual(a.all(), want) || !reflect.DeepEqual(b.all(), want) || len(other.all()) != 0 {
		t.Fatalf("told = %v, %v, %v", a.all(), b.all(), other.all())
	}
}

func TestFolderViewsReadAgainWhenItsSpaceChangesWhileItAttaches(t *testing.T) {
	views := NewFolderViews()
	reads := 0
	change := func() error { return views.Changed("s", background) }
	views.Bind(func(string, string, context.Context) (FolderListing, error) {
		reads++
		run := change
		change = nil
		if run != nil {
			if err := run(); err != nil {
				return FolderListing{}, err
			}
		}
		return listing(reads), nil
	})
	a := &recorder{}
	attached, err := views.Attach("s", "", a, background)
	if err != nil || !reflect.DeepEqual(attached.Value, listing(2)) {
		t.Fatalf("attach = %+v, %v", attached, err)
	}
	if len(a.all()) != 0 {
		t.Fatalf("told = %v", a.all())
	}
}

func TestFolderViewsNeverReadOrTellADetachedViewAgain(t *testing.T) {
	views := NewFolderViews()
	reads := 0
	views.Bind(func(string, string, context.Context) (FolderListing, error) { reads++; return listing(reads), nil })
	a := &recorder{}
	attached, err := views.Attach("s", "docs", a, background)
	if err != nil {
		t.Fatal(err)
	}
	attached.Detach()
	if err := views.Changed("s", background); err != nil {
		t.Fatal(err)
	}
	if reads != 1 || len(a.all()) != 0 {
		t.Fatalf("reads = %d, told = %v", reads, a.all())
	}
}

func TestFolderViewsKeepNothingOfAViewWhoseFirstReadFails(t *testing.T) {
	views := NewFolderViews()
	fail := true
	reads := 0
	views.Bind(func(string, string, context.Context) (FolderListing, error) {
		reads++
		if fail {
			return FolderListing{}, errors.New("read failed")
		}
		return listing(reads), nil
	})
	if _, err := views.Attach("s", "", &recorder{}, background); err == nil || err.Error() != "read failed" {
		t.Fatalf("attach = %v", err)
	}
	fail = false
	if err := views.Changed("s", background); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("reads = %d, want 1", reads)
	}
}
