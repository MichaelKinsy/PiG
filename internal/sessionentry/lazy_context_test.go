package sessionentry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// cancelingRecords is a source whose first read cancels a context, and counts its reads.
type cancelingRecords struct {
	memRecords
	cancel context.CancelFunc
	reads  int
}

func (c *cancelingRecords) ReadRecords(ats []sessionentry.RecordAt, out []json.RawMessage) (int, error) {
	c.reads++
	c.cancel()
	return c.memRecords.ReadRecords(ats, out)
}

// MaterializeAllContext checks its context before the read and between the runs of records of one source each, and returns the context's
// error and no entries; the entries are the ones MaterializeAll returns while the context lives.
func TestMaterializeAllContextStopsBetweenSources(t *testing.T) {
	record := json.RawMessage(`{"type":"message","id":"a","parentId":null,"timestamp":"t","message":{"role":"user","content":"x","timestamp":1}}`)
	ctx, cancel := context.WithCancel(context.Background())
	first := &cancelingRecords{records: []json.RawMessage{record}, cancel: cancel}
	second := &memRecords{records: []json.RawMessage{record}}
	a := sessionentry.NewLazyEntry("a", nil, "t", first, sessionentry.RecordAt{})
	b := sessionentry.NewLazyEntry("a", nil, "t", second, sessionentry.RecordAt{})
	entries := []sessionentry.SessionEntry{&a, &b}

	got, err := sessionentry.MaterializeAllContext(ctx, entries)
	if !errors.Is(err, context.Canceled) || got != nil || first.reads != 1 {
		t.Fatalf("a context cancelled by the first source's read: %v, %v after %d reads; want context.Canceled, no entries, 1 read", got, err, first.reads)
	}

	done, stop := context.WithCancel(context.Background())
	stop()
	first.reads = 0
	if got, err := sessionentry.MaterializeAllContext(done, entries); !errors.Is(err, context.Canceled) || got != nil || first.reads != 0 {
		t.Fatalf("a context done before the read: %v, %v after %d reads; want context.Canceled, no entries, no read", got, err, first.reads)
	}

	got, err = sessionentry.MaterializeAllContext(context.Background(), entries)
	if err != nil || len(got) != 2 || got[0].Base().ID != "a" {
		t.Fatalf("a live context: %v, %v", got, err)
	}
}
