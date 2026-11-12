package codingagent

import (
	"context"
	"slices"
	"sync"
	"testing"
)

// releaseProbe stands in for the release of the units a warm-up keeps for the next one. It records the texts released.
type releaseProbe struct {
	mu    sync.Mutex
	texts []string
}

func newReleaseProbe(t *testing.T) *releaseProbe {
	t.Helper()
	probe := &releaseProbe{}
	previous := writeWarmRelease
	writeWarmRelease = func(code, _ string) {
		probe.mu.Lock()
		defer probe.mu.Unlock()
		probe.texts = append(probe.texts, code)
	}
	t.Cleanup(func() { writeWarmRelease = previous })
	return probe
}

func (probe *releaseProbe) released() []string {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return slices.Clone(probe.texts)
}

// The units a warm-up keeps are for the next warm-up, so a card that still streams keeps them.
func TestWriteWarmUpKeepsItsUnitsWhileTheCallStreams(t *testing.T) {
	warm := newWarmProbe(t)
	close(warm.release)
	released := newReleaseProbe(t)
	content := streamedWriteTypeScript(60)
	stream := newWriteStream(t, context.Background())
	for end := 1000; end < len(content); end += 1000 {
		stream.draw(content[:end], false)
		stream.state.tasks.Wait()
	}
	if len(warm.texts) < 2 {
		t.Fatalf("%d warm-ups: the card did not stream in steps", len(warm.texts))
	}
	if got := released.released(); len(got) != 0 {
		t.Errorf("a card that still streams released the units of %d texts", len(got))
	}
}

// The frame that completes the call draws the highlight of the whole file, which continues from the state the last warm-up left and
// takes its units. The call that completes while a warm-up runs draws first, and the warm-up ends after it: its units are for no
// text that follows, so the warm-up releases them when it ends.
func TestWriteWarmUpReleasesItsUnitsWhenTheCallCompletesDuringAStep(t *testing.T) {
	warm := newWarmProbe(t)
	released := newReleaseProbe(t)
	content := streamedWriteTypeScript(60)
	stream := newWriteStream(t, context.Background())
	stream.draw(content[:9000], false)
	<-warm.entered
	stream.draw(content[:18000], true)
	if got := released.released(); len(got) != 0 {
		t.Fatalf("units of %d texts were released while the warm-up still ran", len(got))
	}
	close(warm.release)
	stream.state.tasks.Wait()
	if got := released.released(); !slices.Equal(got, warm.texts[:1]) {
		t.Errorf("released %d texts, want the one text the warm-up ran over", len(got))
	}
}

// The card's disposal and its session's end stop the warm-up, and the step that runs releases the units it keeps.
func TestWriteWarmUpReleasesItsUnitsWhenTheCardEndsDuringAStep(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(*writeStream, context.CancelFunc)
	}{
		{"disposed", func(stream *writeStream, _ context.CancelFunc) {
			stream.state.mu.Lock()
			stream.state.closed = true
			stream.state.mu.Unlock()
		}},
		{"session ended", func(_ *writeStream, cancel context.CancelFunc) { cancel() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warm := newWarmProbe(t)
			released := newReleaseProbe(t)
			content := streamedWriteTypeScript(60)
			lifetime, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream := newWriteStream(t, lifetime)
			stream.draw(content[:9000], false)
			<-warm.entered
			tc.end(stream, cancel)
			close(warm.release)
			stream.state.tasks.Wait()
			if got := released.released(); !slices.Equal(got, warm.texts[:1]) {
				t.Errorf("released %d texts, want the one text the warm-up ran over", len(got))
			}
		})
	}
}

// A call that completes after its last warm-up ended draws the highlight of the whole file, and the card releases what that draw left.
func TestWriteWarmUpReleasesItsUnitsAfterTheDrawOfTheCompleteCall(t *testing.T) {
	warm := newWarmProbe(t)
	close(warm.release)
	released := newReleaseProbe(t)
	content := streamedWriteTypeScript(60)
	stream := newWriteStream(t, context.Background())
	stream.draw(content[:9000], false)
	stream.state.tasks.Wait()
	if got := released.released(); len(got) != 0 {
		t.Fatalf("units were released before the call completed")
	}
	stream.draw(content, true)
	if got := released.released(); !slices.Equal(got, warm.texts[:1]) {
		t.Errorf("released %d texts, want the one text the warm-up ran over", len(got))
	}
	stream.draw(content, true)
	if got := released.released(); len(got) != 1 {
		t.Errorf("a second draw of the complete call released again: %d releases", len(got))
	}
}

// A card disposed between warm-ups, as one whose call never completes is, releases the units its last warm-up kept, once.
func TestWriteWarmUpReleasesItsUnitsWhenAnIdleCardIsDisposed(t *testing.T) {
	warm := newWarmProbe(t)
	close(warm.release)
	released := newReleaseProbe(t)
	content := streamedWriteTypeScript(60)
	stream := newWriteStream(t, context.Background())
	stream.draw(content[:9000], false)
	stream.state.tasks.Wait()
	card := &ToolRendererCard{state: stream.state, cancel: func() {}}
	card.Dispose()
	card.Dispose()
	if got := released.released(); !slices.Equal(got, warm.texts[:1]) {
		t.Errorf("released %d texts, want the one text the warm-up ran over", len(got))
	}
}

// A warm-up that cannot resume keeps no units, so there is nothing to release.
func TestWriteWarmUpThatCannotResumeReleasesNothing(t *testing.T) {
	warm := newWarmProbe(t)
	close(warm.release)
	warm.resumable = false
	released := newReleaseProbe(t)
	content := streamedWriteTypeScript(60)
	stream := newWriteStream(t, context.Background())
	stream.draw(content[:9000], false)
	stream.state.tasks.Wait()
	stream.draw(content, true)
	(&ToolRendererCard{state: stream.state, cancel: func() {}}).Dispose()
	if got := released.released(); len(got) != 0 {
		t.Errorf("released %d texts of a warm-up that held no state", len(got))
	}
}
