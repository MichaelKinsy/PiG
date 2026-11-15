package store

import "github.com/MichaelKinsy/PiG/chord/next/internal/storeseam"

func init() {
	storeseam.QueueTail = func(chordStore any) <-chan struct{} { return chordStore.(*ChordStore).queueTail() }
	storeseam.ReadTasks = func(chordStore any) <-chan struct{} { return chordStore.(*ChordStore).Reads.idleChan() }
	storeseam.WrapAdopt = func(chordStore any, wrap func(adopt storeseam.Adopt) storeseam.Adopt) {
		store := chordStore.(*ChordStore)
		store.mu.Lock()
		store.wrapAdopt = wrap
		store.mu.Unlock()
	}
}

// queueTail is the store's commit queue tail: it changes whenever an entry takes its place.
func (store *ChordStore) queueTail() <-chan struct{} {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.tail
}
