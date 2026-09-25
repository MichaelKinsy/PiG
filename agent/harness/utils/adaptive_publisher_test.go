package utils

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func publisherIntervals(minInterval, target float64) (*float64, *float64) {
	return &minInterval, &target
}

func TestAdaptivePublisherBoundsEventCountAndSpacesLargePublicationsByEncodedSize(t *testing.T) {
	clock := &fakeClock{}
	value := "a"
	var updates []string
	minInterval, target := publisherIntervals(100, 100)
	publisher := newAdaptivePublisher(AdaptivePublisherOptions[string, string]{
		Snapshot:             func() string { return value },
		Update:               func(_ *string, current string) (string, bool) { return current, true },
		Measure:              func(update string) int { return len(update) },
		Publish:              func(update string) error { updates = append(updates, update); return nil },
		OnError:              func(err error) { t.Fatalf("unexpected error: %v", err) },
		MinIntervalMs:        minInterval,
		TargetBytesPerSecond: target,
	}, clock)

	mustMarkDirty(t, publisher)
	value = strings.Repeat("x", 100)
	mustMarkDirty(t, publisher)
	clock.advance(100)
	if !reflect.DeepEqual(updates, []string{"a", strings.Repeat("x", 100)}) {
		t.Fatalf("updates = %q", updates)
	}

	value = "held"
	mustMarkDirty(t, publisher)
	clock.advance(999)
	if len(updates) != 2 {
		t.Fatalf("published before its size-bought delay: %q", updates)
	}
	clock.advance(1)
	if !reflect.DeepEqual(updates, []string{"a", strings.Repeat("x", 100), "held"}) {
		t.Fatalf("updates = %q", updates)
	}
}

func mustMarkDirty[TValue, TUpdate any](t *testing.T, publisher *AdaptivePublisher[TValue, TUpdate]) {
	t.Helper()
	if err := publisher.MarkDirty(); err != nil {
		t.Fatalf("MarkDirty: %v", err)
	}
}

type publication struct {
	previous *string
	current  string
}

func TestAdaptivePublisherCommitsItsBaselineBeforeAConsumerThrows(t *testing.T) {
	clock := &fakeClock{}
	value := "a"
	var updates []publication
	throwAfterApply := false
	minInterval, target := publisherIntervals(100, 100)
	publisher := newAdaptivePublisher(AdaptivePublisherOptions[string, publication]{
		Snapshot: func() string { return value },
		Update: func(previous *string, current string) (publication, bool) {
			return publication{previous: previous, current: current}, true
		},
		Measure: func(publication) int { return 1 },
		Publish: func(update publication) error {
			updates = append(updates, update)
			if throwAfterApply {
				return errors.New("consumer failed after apply")
			}
			return nil
		},
		OnError:              func(error) {},
		MinIntervalMs:        minInterval,
		TargetBytesPerSecond: target,
	}, clock)

	mustMarkDirty(t, publisher)
	clock.advance(100)
	value = "ab"
	throwAfterApply = true
	if err := publisher.MarkDirty(); err == nil || err.Error() != "consumer failed after apply" {
		t.Fatalf("MarkDirty err = %v", err)
	}
	if err := publisher.Flush(true); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	assertPublications(t, updates, []string{"", "a"}, []string{"a", "ab"})

	throwAfterApply = false
	clock.advance(100)
	value = "abc"
	mustMarkDirty(t, publisher)
	last := updates[len(updates)-1]
	if last.previous == nil || *last.previous != "ab" || last.current != "abc" {
		t.Fatalf("last publication = %+v", last)
	}
}

func assertPublications(t *testing.T, updates []publication, pairs ...[]string) {
	t.Helper()
	if len(updates) != len(pairs) {
		t.Fatalf("publications = %d, want %d", len(updates), len(pairs))
	}
	for index, pair := range pairs {
		previous := ""
		if updates[index].previous != nil {
			previous = *updates[index].previous
		}
		if (pair[0] == "") != (updates[index].previous == nil) || previous != pair[0] || updates[index].current != pair[1] {
			t.Fatalf("publication %d = %v -> %q, want %q -> %q", index, updates[index].previous, updates[index].current, pair[0], pair[1])
		}
	}
}

func TestAdaptivePublisherReportsTrailingTimerErrorsAndStopsAfterDispose(t *testing.T) {
	clock := &fakeClock{}
	var reported []error
	failure := errors.New("publish failed")
	calls := 0
	publisher := newAdaptivePublisher(AdaptivePublisherOptions[int, int]{
		Snapshot: func() int { return calls },
		Update:   func(_ *int, current int) (int, bool) { return current, true },
		Measure:  func(int) int { return 1 },
		Publish: func(int) error {
			calls++
			if calls == 2 {
				return failure
			}
			return nil
		},
		OnError: func(err error) { reported = append(reported, err) },
	}, clock)
	mustMarkDirty(t, publisher)
	mustMarkDirty(t, publisher)
	clock.advance(100)
	if len(reported) != 1 || !errors.Is(reported[0], failure) {
		t.Fatalf("reported = %v", reported)
	}
	mustMarkDirty(t, publisher)
	publisher.Dispose()
	clock.advance(1_000)
	if calls != 2 {
		t.Fatalf("published after dispose: calls = %d", calls)
	}
	if err := publisher.Flush(true); err != nil || calls != 2 {
		t.Fatalf("flush after dispose: err=%v calls=%d", err, calls)
	}
}

func TestAdaptivePublisherSkipsUnchangedUpdatesButAdvancesTheBaseline(t *testing.T) {
	clock := &fakeClock{}
	value := 1
	var seen []*int
	publisher := newAdaptivePublisher(AdaptivePublisherOptions[int, int]{
		Snapshot: func() int { return value },
		Update: func(previous *int, current int) (int, bool) {
			seen = append(seen, previous)
			return current, current != 2
		},
		Measure: func(int) int { return 0 },
		Publish: func(int) error { return nil },
	}, clock)
	mustMarkDirty(t, publisher)
	clock.advance(100)
	value = 2
	mustMarkDirty(t, publisher)
	value = 3
	mustMarkDirty(t, publisher)
	if len(seen) != 3 || seen[2] == nil || *seen[2] != 2 {
		t.Fatalf("baselines = %v", seen)
	}
}

func TestAdaptivePublisherDefersReentrantDeliveryToTheTrailingTimer(t *testing.T) {
	clock := &fakeClock{}
	value := 1
	var published []int
	var publisher *AdaptivePublisher[int, int]
	publisher = newAdaptivePublisher(AdaptivePublisherOptions[int, int]{
		Snapshot: func() int { return value },
		Update:   func(_ *int, current int) (int, bool) { return current, true },
		Measure:  func(int) int { return 0 },
		Publish: func(update int) error {
			published = append(published, update)
			if update == 1 {
				value = 2
				return publisher.MarkDirty()
			}
			return nil
		},
	}, clock)
	mustMarkDirty(t, publisher)
	if len(published) != 1 {
		t.Fatalf("reentrant delivery ran inline: %v", published)
	}
	clock.advance(100)
	if !reflect.DeepEqual(published, []int{1, 2}) {
		t.Fatalf("published = %v", published)
	}
}
