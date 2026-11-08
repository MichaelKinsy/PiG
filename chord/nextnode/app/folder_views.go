package app

import (
	"context"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/src/node/app/folder-views.ts
//
// Live folder listings (Files v2): what a `FilesFolder` object shows, the first entries of one folder, followed
// (`self.follow`) from the host's `Files`. Files reads a watched folder again after each change of its space, inside the
// method that changed it, and hands the listing to its watchers; a listing attaching meanwhile reads again. One read per
// watched folder and change: meant for the folders on someone's screen, not for every folder of a drive.
//
// Go forced differences: the listing is typed (FolderListing) and handed to observers as its JSON form; the awaited reads
// block and return `(value, error)`.

// FolderListing is a folder's first entries, and whether more follow.
type FolderListing struct {
	Entries []FolderEntry
	More    bool
}

// JSON is the listing as the `FilesFolder` member has it.
func (listing FolderListing) JSON() *delta.JsonObject {
	entries := make([]any, len(listing.Entries))
	for index, entry := range listing.Entries {
		entries[index] = entry.JSON()
	}
	return delta.JsonObjectOf("entries", entries, "more", listing.More)
}

// ListingReader is how a folder's listing is read: the current `Files` attempt's index.
type ListingReader func(space, folder string, ctx context.Context) (FolderListing, error)

// view is a watched folder: its observer, and whether it is still attaching (then a change makes it read again).
type view struct {
	space, folder string
	observer      host.FollowObserver[any]
	attaching     bool
	stale         bool
}

// FolderAttachment is what attaching a view gives: the listing now, and its detach.
type FolderAttachment struct {
	Value  FolderListing
	Detach func()
}

// FolderViews are the watched folders of one host's `Files`.
type FolderViews struct {
	mu     sync.Mutex
	reader ListingReader
	views  []*view
}

// NewFolderViews is no watched folder.
func NewFolderViews() *FolderViews { return &FolderViews{} }

// Bind reads through reader from now on (each `Files` attempt binds its own).
func (views *FolderViews) Bind(reader ListingReader) {
	views.mu.Lock()
	defer views.mu.Unlock()
	views.reader = reader
}

// Attach watches folder folder of space for observer: its listing now, and its detach.
func (views *FolderViews) Attach(space, folder string, observer host.FollowObserver[any], ctx context.Context) (*FolderAttachment, error) {
	each := &view{space: space, folder: folder, observer: observer, attaching: true}
	views.mu.Lock()
	views.views = append(views.views, each)
	views.mu.Unlock()
	var value FolderListing
	for {
		views.mu.Lock()
		each.stale = false
		views.mu.Unlock()
		var err error
		if value, err = views.read(space, folder, ctx); err != nil {
			views.delete(each)
			return nil, err
		}
		views.mu.Lock()
		stale := each.stale
		views.mu.Unlock()
		if !stale {
			break
		}
	}
	views.mu.Lock()
	each.attaching = false
	views.mu.Unlock()
	return &FolderAttachment{Value: value, Detach: func() { views.delete(each) }}, nil
}

func (views *FolderViews) delete(each *view) {
	views.mu.Lock()
	defer views.mu.Unlock()
	for index, other := range views.views {
		if other == each {
			views.views = append(views.views[:index:index], views.views[index+1:]...)
			return
		}
	}
}

func (views *FolderViews) has(each *view) bool {
	views.mu.Lock()
	defer views.mu.Unlock()
	return slices.Contains(views.views, each)
}

// Changed is: space changed: every watched folder of it is read again (once per folder) and its watchers told.
func (views *FolderViews) Changed(space string, ctx context.Context) error {
	type group struct {
		folder string
		views  []*view
	}
	var groups []*group
	views.mu.Lock()
	for _, each := range views.views {
		if each.space != space {
			continue
		}
		if each.attaching {
			each.stale = true
			continue
		}
		var found *group
		for _, candidate := range groups {
			if candidate.folder == each.folder {
				found = candidate
			}
		}
		if found == nil {
			found = &group{folder: each.folder}
			groups = append(groups, found)
		}
		found.views = append(found.views, each)
	}
	views.mu.Unlock()
	for _, each := range groups {
		listing, err := views.read(space, each.folder, ctx)
		if err != nil {
			return err
		}
		value := listing.JSON()
		for _, watcher := range each.views {
			if views.has(watcher) {
				watcher.observer.Advance(chord.WatchCall[any]{
					Kind: chord.WatchCallValue, Value: value, Ops: []delta.Op{{"r", value}},
				}, ctx)
			}
		}
	}
	return nil
}

// read is the listing through the bound reader (bound before any `FilesFolder` goes live: it waits for `Files`).
func (views *FolderViews) read(space, folder string, ctx context.Context) (FolderListing, error) {
	views.mu.Lock()
	reader := views.reader
	views.mu.Unlock()
	return reader(space, folder, ctx)
}
