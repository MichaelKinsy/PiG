package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

type serverJournal struct {
	mu     sync.Mutex
	events []string
}

func (journal *serverJournal) add(event string) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.events = append(journal.events, event)
}
func (journal *serverJournal) snapshot() []string {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	return slices.Clone(journal.events)
}

// server.ts appends every mutation to one recovered Promise tail. Disposal closes endpoints first, then joins the tail; it does not cancel callbacks or discard queued calls.
func TestServerMutationsFIFOAndDisposeDrainFailure(t *testing.T) {
	root, err := filepath.Abs("../../..")
	requireModelsOK(t, err)
	output, err := exec.CommandContext(t.Context(), "node", "--experimental-transform-types", "testdata/server-lifecycle-oracle.mjs", root).Output()
	requireModelsOK(t, err)
	var expected struct {
		Events   []string `json:"events"`
		Updates  int32    `json:"updates"`
		Disposed bool     `json:"disposed"`
	}
	requireModelsOK(t, json.Unmarshal(output, &expected))
	synctest.Test(t, func(t *testing.T) {
		var journal serverJournal
		var listContexts []any
		createCtx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		type contextKey struct{}
		removeCtx := context.WithValue(t.Context(), contextKey{}, "remove")
		refreshCtx := context.WithValue(t.Context(), contextKey{}, "refresh")
		entered, release := make(chan struct{}), make(chan struct{})
		releaseCreate := sync.OnceFunc(func() { close(release) })
		defer releaseCreate()
		services, err := CreateExperimentalServerServices(ExperimentalServerServicesOptions{
			List: func(ctx context.Context) ([]SessionSummary, error) {
				journal.add("list")
				listContexts = append(listContexts, ctx.Value(contextKey{}))
				return []SessionSummary{}, nil
			},
			Create: func(ctx context.Context, _ SessionCreateOptions) (SessionSummary, error) {
				if ctx != createCtx {
					t.Error("lost create context")
				}
				journal.add("create:start")
				close(entered)
				<-release
				journal.add("create:rejected")
				return SessionSummary{}, context.Cause(ctx)
			},
			Remove: func(ctx context.Context, id string) error {
				if ctx != removeCtx {
					t.Error("lost remove context")
				}
				journal.add("remove:" + id)
				return nil
			},
		})
		requireModelsOK(t, err)
		presentation := testServerPresentation{prepare: func(ctx context.Context, id string) error {
			if ctx != removeCtx {
				t.Error("lost prepare context")
			}
			journal.add("prepare:" + id)
			return nil
		}}
		a, err := services.Host.AttachClient(t.Context(), presentation)
		requireModelsOK(t, err)
		b, err := services.Host.AttachClient(t.Context(), presentation)
		requireModelsOK(t, err)
		var updates atomic.Int32
		for _, attachment := range []*RoutedServerServiceAttachment{a, b} {
			_, err := attachment.InvokeService(t.Context(), chord.CreateServiceSubscribeCall("same", SessionDirectoryDefinition.Id(), chord.ServiceSingleton), func(context.Context, string, chord.ServiceProviderUpdate) error { updates.Add(1); return nil })
			requireModelsOK(t, err)
		}
		created, removed, refreshed, disposed := make(chan error, 1), make(chan error, 1), make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := serverCall(createCtx, a, SessionManagementDefinition.Id(), "create", SessionCreateOptions{})
			created <- err
		}()
		<-entered
		go func() {
			_, err := serverCall(removeCtx, b, SessionManagementDefinition.Id(), "remove", "a")
			removed <- err
		}()
		synctest.Wait()
		go func() { refreshed <- services.Refresh(refreshCtx) }()
		synctest.Wait()
		checkModelsEqual(t, journal.snapshot(), []string{"list", "create:start"})
		go func() { disposed <- services.Dispose() }()
		synctest.Wait()
		for _, attachment := range []*RoutedServerServiceAttachment{a, b} {
			_, err := serverCall(t.Context(), attachment, SessionManagementDefinition.Id(), "attach", "late")
			if err == nil || err.Error() != "Server service attachment is released" {
				t.Fatalf("release: %v", err)
			}
		}
		select {
		case err := <-disposed:
			t.Fatalf("dispose returned before queued work: %v", err)
		default:
		}
		failure := errors.New("cancel create")
		cancel(failure)
		releaseCreate()
		synctest.Wait()
		if err := <-created; !errors.Is(err, failure) {
			t.Fatalf("create error: %v", err)
		}
		requireModelsOK(t, <-removed)
		requireModelsOK(t, <-refreshed)
		requireModelsOK(t, <-disposed)
		checkModelsEqual(t, journal.snapshot(), expected.Events)
		checkModelsEqual(t, updates.Load(), expected.Updates)
		checkModelsEqual(t, expected.Disposed, true)
		checkModelsEqual(t, listContexts, []any{nil, "remove", "refresh"})
		checkModelsEqual(t, services.Host.directory.Value().Revision, 3)
		checkModelsEqual(t, len(services.Host.attachments), 0)
		requireModelsOK(t, services.Dispose())
	})
}

func TestServerMutationFailuresRetainDirectoryAndRecover(t *testing.T) {
	failure := errors.New("storage unavailable")
	initial, err := CreateExperimentalServerServices(ExperimentalServerServicesOptions{List: func(context.Context) ([]SessionSummary, error) { return nil, failure }})
	if initial != nil || !errors.Is(err, failure) {
		t.Fatalf("initial list: services=%v err=%v", initial, err)
	}
	stored := []SessionSummary{}
	failList, failCreate, failPrepare, failRemove := false, false, false, false
	var events []string
	services, err := CreateExperimentalServerServices(ExperimentalServerServicesOptions{
		List: func(context.Context) ([]SessionSummary, error) {
			events = append(events, "list")
			if failList {
				return nil, failure
			}
			return slices.Clone(stored), nil
		},
		Create: func(context.Context, SessionCreateOptions) (SessionSummary, error) {
			events = append(events, "create")
			if failCreate {
				return SessionSummary{}, failure
			}
			value := SessionSummary{SessionAddress{"server", "a"}, 1}
			stored = append(stored, value)
			return value, nil
		},
		Remove: func(context.Context, string) error {
			events = append(events, "remove")
			if failRemove {
				return failure
			}
			stored = []SessionSummary{}
			return nil
		},
	})
	requireModelsOK(t, err)
	t.Cleanup(func() { requireModelsOK(t, services.Dispose()) })
	attachment, err := services.Host.AttachClient(t.Context(), testServerPresentation{prepare: func(context.Context, string) error {
		events = append(events, "prepare")
		if failPrepare {
			return failure
		}
		return nil
	}})
	requireModelsOK(t, err)
	directory, err := chord.Use(attachment.provider, SessionDirectoryDefinition)
	requireModelsOK(t, err)
	invokeFailure := func(member string, args ...any) {
		_, err := serverCall(t.Context(), attachment, SessionManagementDefinition.Id(), member, args...)
		if !errors.Is(err, failure) {
			t.Fatalf("%s error: %v", member, err)
		}
	}
	failCreate = true
	invokeFailure("create", SessionCreateOptions{})
	checkModelsEqual(t, events, []string{"list", "create"})
	failCreate, failList = false, true
	invokeFailure("create", SessionCreateOptions{})
	checkModelsEqual(t, directory.State().Value(), &SessionDirectoryState{Revision: 1, Sessions: []SessionSummary{}})
	failList = false
	requireModelsOK(t, services.Refresh(t.Context()))
	prior := directory.State().Value()
	checkModelsEqual(t, prior.Revision, 2)
	checkModelsEqual(t, prior.Sessions, stored)
	events = nil
	failPrepare = true
	invokeFailure("remove", "a")
	checkModelsEqual(t, events, []string{"prepare"})
	failPrepare, failRemove = false, true
	invokeFailure("remove", "a")
	checkModelsEqual(t, events, []string{"prepare", "prepare", "remove"})
	failRemove, failList = false, true
	invokeFailure("remove", "a")
	checkModelsEqual(t, directory.State().Value(), prior)
	failList = false
	requireModelsOK(t, services.Refresh(t.Context()))
	checkModelsEqual(t, directory.State().Value(), &SessionDirectoryState{Revision: 3, Sessions: []SessionSummary{}})
	checkModelsEqual(t, prior, &SessionDirectoryState{Revision: 2, Sessions: []SessionSummary{{SessionAddress{"server", "a"}, 1}}})
}

func TestServerConcurrentAttachmentsRetainIndependentSelections(t *testing.T) {
	services, err := CreateExperimentalServerServices(ExperimentalServerServicesOptions{
		List: func(context.Context) ([]SessionSummary, error) { return []SessionSummary{}, nil },
		PrepareSessionPlugins: func(_ context.Context, _ string, paths []string) (PreparedSessionPlugins, error) {
			return PreparedSessionPlugins{PackagePaths: paths}, nil
		},
		ReloadPresentationPlugins: func(_ context.Context, paths []string) (pico3.JsonValue, error) { return paths, nil },
	})
	requireModelsOK(t, err)
	const count = 128
	attachments := make([]*RoutedServerServiceAttachment, count)
	for i := range attachments {
		attachments[i], err = services.Host.AttachClient(t.Context(), testServerPresentation{})
		requireModelsOK(t, err)
	}
	var work sync.WaitGroup
	for i, attachment := range attachments {
		work.Go(func() {
			path := fmt.Sprint(i)
			_, err := serverCall(t.Context(), attachment, PresentationPluginsDefinition.Id(), "prepareSession", PrepareSessionPluginsRequest{"session", []string{path}})
			if err != nil {
				t.Error(err)
				return
			}
			result, err := serverCall(t.Context(), attachment, PresentationPluginsDefinition.Id(), "reload")
			if err != nil || string(result) != fmt.Sprintf("[%q]", path) {
				t.Errorf("selection %d: result=%s err=%v", i, result, err)
			}
		})
	}
	work.Wait()
	requireModelsOK(t, services.Dispose())
	checkModelsEqual(t, len(services.Host.attachments), 0)
}

func BenchmarkServerServiceLifecycle(b *testing.B) {
	sessions := make([]SessionSummary, 256)
	for i := range sessions {
		sessions[i] = SessionSummary{SessionAddress{"server", fmt.Sprint(i)}, float64(i)}
	}
	for b.Loop() {
		services, err := CreateExperimentalServerServices(ExperimentalServerServicesOptions{List: func(context.Context) ([]SessionSummary, error) { return sessions, nil }})
		if err != nil {
			b.Fatal(err)
		}
		for range 16 {
			attachment, err := services.Host.AttachClient(b.Context(), testServerPresentation{})
			if err != nil {
				b.Fatal(err)
			}
			_, err = attachment.InvokeService(b.Context(), chord.CreateServiceSubscribeCall("directory", SessionDirectoryDefinition.Id(), chord.ServiceSingleton), func(context.Context, string, chord.ServiceProviderUpdate) error { return nil })
			if err != nil {
				b.Fatal(err)
			}
		}
		if err := services.Refresh(b.Context()); err != nil {
			b.Fatal(err)
		}
		if err := services.Dispose(); err != nil {
			b.Fatal(err)
		}
	}
}
