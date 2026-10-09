// Ports packages/durable/test/chord-guide.test.ts (the code of docs/pico-v5-chord-usage.md run against a real Harness).
//
// Every step of the three guide examples runs against a real Harness and MemoryStorage: the Session-wide canvas, the
// conversation-scoped diff reviews and the task job output watch, with the Chord facet host (defineFacet,
// defineService, createFacetHost, provideMany/observe), an in-process remote service transport with
// createRemoteServiceBinding, and the shutdown order of detach, host dispose and Harness close.
//
// Go mapping. Upstream hands a Session document state to Chord as a Chord ReplicatedState: both are one type there. In
// Go the Session's attached state is the chord package's AttachedReplicatedState and the facet host's is
// internal/chord's, so documentSource attaches the second to the first through the Session state's source stream
// (SubscribeSource, upstream services/state.ts getReplicatedStateInternals). A service contract is a Go interface whose
// members the provider classifies by method set; its guarded view and remote client are registered next to its token
// (RegisterServiceView, RegisterRemoteClient). The facet host runs a handler synchronously within the delivery that
// admits an observed instance.

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	chordsvc "github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// documentSource is a ReplicatedStateSource over a Session document state: its snapshot is the state's publication at
// attachment, and each later publication is the next frame with its exact operations. Disposing the attachment disposes
// the Session state.
type documentSource struct {
	state   *chord.AttachedReplicatedState[durable.JsonObject]
	onError func(error)

	mu             sync.Mutex
	snapshotValue  durable.JsonObject
	snapshotCursor int
	frames         []chordsvc.ReplicatedStateSourceFrame
	listener       func(chordsvc.ReplicatedStateSourceFrame)
	// delivering is set while one goroutine drains frames, so they reach the listener once each, in cursor order.
	delivering  bool
	disposed    bool
	unsubscribe func()
}

func jsonOfObject(object durable.JsonObject) chordsvc.JsonValue {
	if object == nil {
		return nil
	}
	return object
}

// Attach captures the state's publication and buffers every later one. Subscribing before the snapshot leaves no
// publication between them; receive waits for the snapshot under mu. The value and cursor are read together, so a
// publication after Attach is a buffered frame and never part of the snapshot.
func (source *documentSource) Attach() chordsvc.ReplicatedStateSourceAttachment {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.unsubscribe = source.state.SubscribeSource(source.receive)
	source.snapshotValue, source.snapshotCursor = source.state.InternalSnapshot()
	return source
}

// Snapshot is the fixed snapshot Attach captured (internal/chord ReplicatedStateSourceAttachment.Snapshot).
func (source *documentSource) Snapshot() chordsvc.ReplicatedStateSourceSnapshot {
	source.mu.Lock()
	defer source.mu.Unlock()
	return chordsvc.ReplicatedStateSourceSnapshot{Value: jsonOfObject(source.snapshotValue), Cursor: source.snapshotCursor}
}

func (source *documentSource) Activate(listener func(chordsvc.ReplicatedStateSourceFrame)) {
	source.mu.Lock()
	source.listener = listener
	source.mu.Unlock()
	source.drain()
}

// receive runs inside the publication, so the state's latest value is the one these operations produced.
func (source *documentSource) receive(ops []durable.Op, sequence int, ctx context.Context) {
	value, _ := source.state.InternalSnapshot()
	batch := make([]chordsvc.Op, len(ops))
	for index, op := range ops {
		stored, err := chordjson.Stored([]any(op))
		if err != nil {
			source.onError(err)
			return
		}
		batch[index] = chordsvc.Op(stored.([]any))
	}
	source.mu.Lock()
	if source.disposed || sequence <= source.snapshotCursor {
		source.mu.Unlock()
		return
	}
	source.frames = append(source.frames, chordsvc.ReplicatedStateSourceFrame{Cursor: sequence, Value: jsonOfObject(value), Ops: batch, Context: ctx})
	source.mu.Unlock()
	source.drain()
}

func (source *documentSource) drain() {
	source.mu.Lock()
	if source.delivering || source.listener == nil {
		source.mu.Unlock()
		return
	}
	source.delivering = true
	for !source.disposed && len(source.frames) > 0 {
		frame := source.frames[0]
		source.frames = source.frames[1:]
		listener := source.listener
		source.mu.Unlock()
		listener(frame)
		source.mu.Lock()
	}
	source.delivering = false
	source.mu.Unlock()
}

func (source *documentSource) Dispose() {
	source.mu.Lock()
	if source.disposed {
		source.mu.Unlock()
		return
	}
	source.disposed = true
	source.frames = nil
	source.mu.Unlock()
	source.unsubscribe()
	source.state.Dispose()
}

// replicatedStateOf is a Session document state as a Chord replicated state of T; a retired document reads as nil.
func replicatedStateOf[T any](t *testing.T, state *chord.AttachedReplicatedState[durable.JsonObject]) *chordsvc.AttachedReplicatedState[*T] {
	t.Helper()
	attached, err := chordsvc.AttachReplicatedState[*T](&documentSource{state: state, onError: func(err error) { t.Error(err) }}, chordsvc.ReplicatedStateSourceOptions{OnError: func(err error) { t.Error(err) }})
	if err != nil {
		t.Fatal(err)
	}
	return attached
}

// delivery is one subscriber callback: its kind, sequence and the value's size (-1 for a retired document).
type delivery struct {
	Kind     string
	Sequence int
	Size     int
}

type deliveryLog struct {
	mu      sync.Mutex
	entries []delivery
}

func (log *deliveryLog) add(entry delivery) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.entries = append(log.entries, entry)
}

func (log *deliveryLog) all() []delivery {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]delivery(nil), log.entries...)
}

func (log *deliveryLog) reset() {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.entries = nil
}

func (log *deliveryLog) len() int { return len(log.all()) }

// ─── 1. A Session-wide canvas ────────────────────────────────────────────────

type guideStroke struct {
	Color  string       `json:"color"`
	Points []guidePoint `json:"points"`
}

type guidePoint struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type canvasState struct {
	Strokes []guideStroke `json:"strokes"`
}

var canvasDoc = durable.DefineDoc(durable.DocDefinition[canvasState]{
	CommonDocDefinition: durable.CommonDocDefinition[canvasState]{
		Kind:    "app.canvas",
		Version: 1,
		// Store a complete base after at most 99 replayed deltas.
		CheckpointWhen: func(_ canvasState, _ []durable.Op, info durable.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 99
		},

		Initial: func() canvasState { return canvasState{Strokes: []guideStroke{}} },
	},
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeSession},
})

type canvasService interface {
	State() chordsvc.ReplicatedStateOf[*canvasState]
	AddStroke(ctx context.Context, stroke guideStroke) error
}

var canvasServiceDefinition = func() chordsvc.ServiceDefinition[canvasService] {
	definition := chordsvc.DefineService[canvasService]("app.canvas")
	chordsvc.RegisterServiceView(definition, func(resolve func() (canvasService, error)) canvasService { return canvasView{resolve} })
	chordsvc.RegisterRemoteClient(definition, func(service *chordsvc.RemoteService) canvasService { return remoteCanvas{service} })
	return definition
}()

type canvasView struct{ resolve func() (canvasService, error) }

func (view canvasView) State() chordsvc.ReplicatedStateOf[*canvasState] {
	return chordsvc.StateView(func() (chordsvc.ReplicatedStateOf[*canvasState], error) {
		target, err := view.resolve()
		if err != nil {
			return nil, err
		}
		return target.State(), nil
	})
}

func (view canvasView) AddStroke(ctx context.Context, stroke guideStroke) error {
	target, err := view.resolve()
	if err != nil {
		return err
	}
	return target.AddStroke(ctx, stroke)
}

type remoteCanvas struct{ service *chordsvc.RemoteService }

func (canvas remoteCanvas) State() chordsvc.ReplicatedStateOf[*canvasState] {
	replica, err := canvas.service.State("state")
	if err != nil {
		panic(err)
	}
	return chordsvc.TypedReplica[*canvasState](replica)
}

func (canvas remoteCanvas) AddStroke(ctx context.Context, stroke guideStroke) error {
	_, err := canvas.service.Call(ctx, "addStroke", stroke)
	return err
}

type canvasProvider struct {
	session durable.Session
	state   *chordsvc.AttachedReplicatedState[*canvasState]
}

func (canvas *canvasProvider) State() chordsvc.ReplicatedStateOf[*canvasState] { return canvas.state }

func (canvas *canvasProvider) AddStroke(ctx context.Context, stroke guideStroke) error {
	_, err := durable.Commit(ctx, canvas.session, func(tx durable.Tx) (struct{}, error) {
		draft, err := durable.TxDoc[canvasState](tx, canvasDoc)
		if err != nil {
			return struct{}{}, err
		}
		value, err := durable.ToJsonValue(stroke)
		if err != nil {
			return struct{}{}, err
		}
		_, err = draft.Array("strokes").Push(value) // Chord copies the assigned stroke by value.
		return struct{}{}, err
	})
	return err
}

func createCanvasFacet(t *testing.T, ctx context.Context, session durable.Session) chordsvc.Facet {
	t.Helper()
	// Creation is explicit; observation never writes.
	if _, err := durable.Commit(ctx, session, func(tx durable.Tx) (struct{}, error) {
		_, err := durable.TxDoc[canvasState](tx, canvasDoc)
		return struct{}{}, err
	}); err != nil {
		t.Fatal(err)
	}
	var state durable.DocumentState[canvasState]
	state, err := session.DocumentStateErased(ctx, canvasDoc)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil {
		t.Fatal("canvas was retired during setup")
	}
	attached := replicatedStateOf[canvasState](t, state)
	return chordsvc.DefineFacet(chordsvc.Facet{
		Id: "app.canvas/session",
		Setup: func(env *chordsvc.FacetEnvironment) error {
			if err := env.Own(func(context.Context) error { attached.Dispose(); return nil }); err != nil {
				return err
			}
			return chordsvc.ProvideService[canvasService](env, canvasServiceDefinition, &canvasProvider{session: session, state: attached})
		},
	})
}

// canvasConsumer subscribes to the canvas after activation and logs each delivery.
func canvasConsumer(log *deliveryLog) chordsvc.Facet {
	return chordsvc.DefineFacet(chordsvc.Facet{
		Id: "app.canvas/consumer",
		Setup: func(env *chordsvc.FacetEnvironment) error {
			canvas, err := chordsvc.UseService(env, canvasServiceDefinition) // Declare now; access only after activation.
			if err != nil {
				return err
			}
			return env.OnActivate(func(context.Context) error {
				service, err := canvas.Get()
				if err != nil {
					return err
				}
				stop, err := service.State().Subscribe(func(value *canvasState, _ context.Context, d chordsvc.ReplicatedStateDelivery) {
					log.add(delivery{d.Kind, d.Sequence, canvasSize(value)})
				}) // subscribe delivers the current hydrated value, then updates.
				if err != nil {
					return err
				}
				return env.Own(func(context.Context) error { stop(); return nil })
			})
		},
	})
}

func canvasSize(value *canvasState) int {
	if value == nil {
		return -1
	}
	return len(value.Strokes)
}

func runCanvasExample(t *testing.T, ctx context.Context, session durable.Session, log *deliveryLog) {
	t.Helper()
	provider := createCanvasFacet(t, ctx, session)
	host, err := chordsvc.CreateFacetHost(ctx, chordsvc.FacetOptions{Facets: []chordsvc.Facet{provider, canvasConsumer(log)}, OnError: func(err error) { t.Error(err) }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := host.Dispose(ctx); err != nil { // Unsubscribes; does not delete the canvas or close Session.
			t.Error(err)
		}
	}()
	canvas, err := chordsvc.Use(host.Services(), canvasServiceDefinition)
	if err != nil {
		t.Fatal(err)
	}
	if err := canvas.AddStroke(ctx, guideStroke{Color: "black", Points: []guidePoint{{10, 20}, {30, 40}}}); err != nil {
		t.Fatal(err)
	}
	// No wait before the deferred dispose, as upstream: the Session state attachment has delivered the update to the
	// consumer before the commit returns.
}

// inProcess is an in-process transport to a host's service provider; a real client puts a socket between the two.
type inProcess struct{ host *chordsvc.FacetHost }

func (transport inProcess) Invoke(ctx context.Context, call chordsvc.ServiceCall) (json.RawMessage, error) {
	return transport.host.Services().Invoke(ctx, call)
}

func (transport inProcess) Subscribe(_ context.Context, serviceId string, mode chordsvc.ServiceMode, listener chordsvc.UpdateListener) (chordsvc.ServiceSubscription, error) {
	return transport.host.Services().Subscribe(serviceId, mode, listener)
}

func connectCanvas(ctx context.Context, transport chordsvc.RemoteServiceTransport, log *deliveryLog) (func(context.Context) error, error) {
	services, err := chordsvc.CreateRemoteServiceBinding(chordsvc.RemoteServiceBindingOptions{Services: chordsvc.ServiceIDs(canvasServiceDefinition.Id()), Transport: transport})
	if err != nil {
		return nil, err
	}
	remote, err := chordsvc.UseRemote(services, canvasServiceDefinition)
	if err != nil {
		return nil, err
	}
	stop, err := remoteCanvas{remote}.State().Subscribe(func(value *canvasState, _ context.Context, d chordsvc.ReplicatedStateDelivery) {
		log.add(delivery{d.Kind, d.Sequence, canvasSize(value)})
	})
	if err != nil {
		return nil, err
	}
	if err := services.Ready(ctx); err != nil { // Initial snapshot installed; not all future updates.
		stop()
		return nil, errors.Join(err, services.Dispose(context.Background()))
	}
	return func(context.Context) error {
		stop()
		return services.Dispose(context.Background())
	}, nil
}

type disposer interface {
	Dispose(ctx context.Context) error
}

type closer interface {
	Close(ctx context.Context) error
}

func shutdown(ctx context.Context, host disposer, detachClients func(context.Context) error, harness closer) error {
	if err := detachClients(ctx); err != nil {
		return err
	}
	if err := host.Dispose(ctx); err != nil {
		return err
	}
	return harness.Close(ctx)
}

// orderedHarness records the order of Close; orderedHost that of Dispose.
type orderedHarness struct {
	Harness
	order *[]string
}

func (tracked orderedHarness) Close(ctx context.Context) error {
	*tracked.order = append(*tracked.order, "close start")
	err := tracked.Harness.Close(ctx)
	*tracked.order = append(*tracked.order, "close end")
	return err
}

type orderedHost struct {
	host  *chordsvc.FacetHost
	order *[]string
}

func (tracked orderedHost) Dispose(ctx context.Context) error {
	*tracked.order = append(*tracked.order, "dispose start")
	err := tracked.host.Dispose(ctx)
	*tracked.order = append(*tracked.order, "dispose end")
	return err
}

// ─── 2. Conversation-scoped diff reviews ─────────────────────────────────────

type reviewInput struct {
	Path  string `json:"path"`
	Patch string `json:"patch"`
}

type reviewComment struct {
	Id   string `json:"id"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type reviewState struct {
	Path     string          `json:"path"`
	Patch    string          `json:"patch"`
	Comments []reviewComment `json:"comments"`
}

var reviewDoc = durable.DefineDocFamily(durable.DocFamilyDefinition[reviewState, reviewInput]{
	Family:  true,
	Kind:    "app.diff-review",
	Version: 1,
	CheckpointWhen: func(_ reviewState, _ []durable.Op, info durable.CheckpointInfo) bool {
		return info.DeltasSinceBase >= 49
	},
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkCurrent},
	Initial: func(seed reviewInput) reviewState {
		return reviewState{Path: seed.Path, Patch: seed.Patch, Comments: []reviewComment{}}
	},
})

type reviewIdentity struct {
	ConversationId durable.ConversationId `json:"conversationId"`
	Key            string                 `json:"key"`
}

type diffReviewService interface {
	State() chordsvc.ReplicatedStateOf[*reviewState]
	Identity(ctx context.Context) (reviewIdentity, error)
	AddComment(ctx context.Context, comment reviewComment) error
}

var diffReviewsDefinition = func() chordsvc.ServiceDefinition[diffReviewService] {
	definition := chordsvc.DefineService[diffReviewService]("app.diff-reviews")
	chordsvc.RegisterServiceView(definition, func(resolve func() (diffReviewService, error)) diffReviewService { return diffReviewView{resolve} })
	return definition
}()

type diffReviewView struct {
	resolve func() (diffReviewService, error)
}

func (view diffReviewView) State() chordsvc.ReplicatedStateOf[*reviewState] {
	return chordsvc.StateView(func() (chordsvc.ReplicatedStateOf[*reviewState], error) {
		target, err := view.resolve()
		if err != nil {
			return nil, err
		}
		return target.State(), nil
	})
}

func (view diffReviewView) Identity(ctx context.Context) (reviewIdentity, error) {
	target, err := view.resolve()
	if err != nil {
		return reviewIdentity{}, err
	}
	return target.Identity(ctx)
}

func (view diffReviewView) AddComment(ctx context.Context, comment reviewComment) error {
	target, err := view.resolve()
	if err != nil {
		return err
	}
	return target.AddComment(ctx, comment)
}

type reviewProvider struct {
	session        durable.Session
	conversationId durable.ConversationId
	key            string
	seed           reviewInput
	state          *chordsvc.AttachedReplicatedState[*reviewState]
}

func (review *reviewProvider) State() chordsvc.ReplicatedStateOf[*reviewState] { return review.state }

func (review *reviewProvider) Identity(context.Context) (reviewIdentity, error) {
	return reviewIdentity{ConversationId: review.conversationId, Key: review.key}, nil
}

func (review *reviewProvider) AddComment(ctx context.Context, comment reviewComment) error {
	_, err := durable.Commit(ctx, review.session, func(tx durable.Tx) (struct{}, error) {
		seed, err := durable.ToJsonValue(review.seed)
		if err != nil {
			return struct{}{}, err
		}
		draft, err := durable.TxDoc[reviewState](tx, reviewDoc, review.conversationId, review.key, seed)
		if err != nil {
			return struct{}{}, err
		}
		value, err := durable.ToJsonValue(comment)
		if err != nil {
			return struct{}{}, err
		}
		_, err = draft.Array("comments").Push(value) // Chord copies the assigned comment by value.
		return struct{}{}, err
	})
	return err
}

func reviewFacet(t *testing.T, ctx context.Context, session durable.Session, conversationId durable.ConversationId, reviews []struct {
	key  string
	seed reviewInput
}) chordsvc.Facet {
	return chordsvc.DefineFacet(chordsvc.Facet{
		Id: "app.diff-reviews/session",
		Setup: func(env *chordsvc.FacetEnvironment) error {
			instances, err := chordsvc.ProvideMany(env, diffReviewsDefinition)
			if err != nil {
				return err
			}
			return env.OnActivate(func(context.Context) error {
				for _, review := range reviews {
					seed, err := durable.ToJsonValue(review.seed)
					if err != nil {
						return err
					}
					if _, err := durable.Commit(ctx, session, func(tx durable.Tx) (struct{}, error) {
						_, err := durable.TxDoc[reviewState](tx, reviewDoc, conversationId, review.key, seed)
						return struct{}{}, err
					}); err != nil {
						return err
					}
					state, err := session.DocumentStateErased(ctx, reviewDoc, conversationId, review.key)
					if err != nil {
						return err
					}
					if state == nil {
						return errors.New("review was retired during setup")
					}
					attached := replicatedStateOf[reviewState](t, state)
					if err := env.Own(func(context.Context) error { attached.Dispose(); return nil }); err != nil {
						return err
					}
					// Chord instance keys route services; they are not numeric document incarnation IDs.
					key, err := json.Marshal([]any{conversationId, review.key})
					if err != nil {
						return err
					}
					// The facet owns spawned service lifetimes automatically.
					if _, err := instances.Spawn(string(key), diffReviewService(&reviewProvider{session: session, conversationId: conversationId, key: review.key, seed: review.seed, state: attached})); err != nil {
						return err
					}
				}
				return nil
			})
		},
	})
}

func TestChordUsageGuide(t *testing.T) {
	// chord-guide.test.ts:269
	t.Run("runs the canvas: facet host, a remote client, withdrawal and detach before Harness close", func(t *testing.T) {
		log := &deliveryLog{}
		harness, _, _ := openTasks(t, storage.NewMemoryStorage(), nil)
		runCanvasExample(t, testContext, harness, log)
		want := []delivery{{"hydrate", 0, 0}, {"update", 1, 1}}
		if got := log.all(); !reflect.DeepEqual(got, want) {
			t.Fatalf("deliveries = %+v, want %+v", got, want)
		}
		if snapshot, err := durable.Snapshot[canvasState](testContext, harness, canvasDoc); err != nil || snapshot == nil || len(snapshot.Strokes) != 1 {
			t.Fatalf("snapshot = %+v, %v", snapshot, err)
		}

		// A worker installs the canvas facet again; a late remote client hydrates the stroke, then follows updates.
		log.reset()
		host, err := chordsvc.CreateFacetHost(testContext, chordsvc.FacetOptions{Facets: []chordsvc.Facet{createCanvasFacet(t, testContext, harness)}, OnError: func(err error) { t.Error(err) }})
		if err != nil {
			t.Fatal(err)
		}
		detach, err := connectCanvas(testContext, inProcess{host}, log)
		if err != nil {
			t.Fatal(err)
		}
		canvas, err := chordsvc.Use(host.Services(), canvasServiceDefinition)
		if err != nil {
			t.Fatal(err)
		}
		if err := canvas.AddStroke(testContext, guideStroke{Color: "red", Points: []guidePoint{}}); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool { return log.len() == 2 })
		got := log.all()
		if want := []delivery{{"hydrate", 0, 1}, {"update", 1, 2}}; got[0].Kind != want[0].Kind || got[0].Size != want[0].Size || got[1].Kind != want[1].Kind || got[1].Size != want[1].Size {
			t.Fatalf("late client deliveries = %+v, want kinds and sizes of %+v", got, want)
		}

		// Record the shutdown order: clients detach and services withdraw while the Harness is still open.
		var order []string
		err = shutdown(testContext, orderedHost{host, &order}, func(ctx context.Context) error {
			order = append(order, "detach start")
			err := detach(ctx)
			order = append(order, "detach end")
			return err
		}, orderedHarness{harness, &order})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"detach start", "detach end", "dispose start", "dispose end", "close start", "close end"}; !reflect.DeepEqual(order, want) {
			t.Fatalf("order = %v, want %v", order, want)
		}
		if _, err := chordsvc.Use(host.Services(), canvasServiceDefinition); err == nil || !strings.Contains(err.Error(), "disposed") {
			t.Fatalf("use after dispose: %v", err)
		}
		_, err = durable.Commit(testContext, harness, func(durable.Tx) (struct{}, error) { return struct{}{}, nil })
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("commit after close: %v", err)
		}
	})

	// chord-guide.test.ts:327
	t.Run("runs the diff reviews with a keyed consumer", func(t *testing.T) {
		harness, _, _ := openTasks(t, storage.NewMemoryStorage(), nil)
		root, err := harness.Root(testContext, nil)
		if err != nil {
			t.Fatal(err)
		}
		reviews := []struct {
			key  string
			seed reviewInput
		}{{"review-7", reviewInput{Path: "a.ts", Patch: "-old\n+new"}}}
		type seenFrame struct {
			Key      string
			Comments int
		}
		var mu sync.Mutex
		var seen []seenFrame
		consumer := chordsvc.DefineFacet(chordsvc.Facet{
			Id: "app.diff-reviews/consumer",
			Setup: func(env *chordsvc.FacetEnvironment) error {
				return chordsvc.ObserveService(env, diffReviewsDefinition, func(ctx context.Context, review diffReviewService) error {
					identity, err := review.Identity(ctx)
					if err != nil {
						return err
					}
					if _, err := review.State().Subscribe(func(value *reviewState, _ context.Context, _ chordsvc.ReplicatedStateDelivery) {
						comments := -1
						if value != nil {
							comments = len(value.Comments)
						}
						mu.Lock()
						defer mu.Unlock()
						seen = append(seen, seenFrame{identity.Key, comments})
					}); err != nil {
						return err
					}
					return review.AddComment(ctx, reviewComment{Id: "c1", Line: 1, Text: "why?"})
				})
			},
		})
		host, err := chordsvc.CreateFacetHost(testContext, chordsvc.FacetOptions{
			Facets:  []chordsvc.Facet{reviewFacet(t, testContext, harness, root.Id(), reviews), consumer},
			OnError: func(err error) { t.Error(err) },
		})
		if err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(seen) >= 2 && seen[len(seen)-1].Comments == 1
		})
		mu.Lock()
		if want := []seenFrame{{"review-7", 0}, {"review-7", 1}}; !reflect.DeepEqual(seen, want) {
			t.Fatalf("seen = %+v, want %+v", seen, want)
		}
		mu.Unlock()
		snapshot, err := durable.Snapshot[reviewState](testContext, harness, reviewDoc, root.Id(), "review-7")
		if err != nil || snapshot == nil || snapshot.Path != "a.ts" || len(snapshot.Comments) != 1 || snapshot.Comments[0].Id != "c1" {
			t.Fatalf("snapshot = %+v %v", snapshot, err)
		}
		if err := host.Dispose(testContext); err != nil {
			t.Fatal(err)
		}
		mustClose(t, harness)
	})

	// chord-guide.test.ts:358
	t.Run("runs the job output watch until the producer retires its document", func(t *testing.T) {
		type jobInput struct {
			Command string `json:"command"`
		}
		type jobOutput struct {
			Stdout string `json:"stdout"`
			Chunks int    `json:"chunks"`
		}
		type jobState struct {
			Phase string `json:"phase"`
		}
		jobOutputDoc := durable.DefineDoc(durable.DocDefinition[jobOutput]{
			CommonDocDefinition: durable.CommonDocDefinition[jobOutput]{
				Kind:           "app.job-output",
				Version:        1,
				CheckpointWhen: func(_ jobOutput, _ []durable.Op, info durable.CheckpointInfo) bool { return info.DeltasSinceBase >= 99 },

				Initial: func() jobOutput { return jobOutput{} },
			},
			DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeTask},
		})
		type runtime = durable.TaskRuntime[jobInput, jobState, durable.JsonValue, any]
		// appendJobOutput: read process output outside this callback; the runtime gates the live task.
		appendJobOutput := func(ctx context.Context, rt runtime, chunk string) error {
			return rt.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[jobInput, jobState, durable.JsonValue]) (*durable.NextTaskState[jobState, durable.JsonValue], error) {
				draft, err := durable.TxDoc[jobOutput](tx, jobOutputDoc, rt.TaskId())
				if err != nil {
					return nil, err
				}
				stdout, _ := draft.Get("stdout").(string)
				chunks, _ := draft.Get("chunks").(float64)
				stdout += chunk
				if len(stdout) > 50_000 {
					stdout = stdout[len(stdout)-50_000:]
				}
				if err := draft.Set("stdout", stdout); err != nil {
					return nil, err
				}
				return nil, draft.Set("chunks", chunks+1)
			})
		}
		gate := deferred()
		job := durable.DefineTask(durable.TaskDefinition[jobInput, jobState, durable.JsonValue, any]{
			Name:    "app.job",
			Version: 1,
			Initial: func(jobInput) jobState { return jobState{Phase: "running"} },
			Phases: map[string]durable.PhaseHandler[jobInput, jobState, durable.JsonValue, any]{
				"running": func(ctx context.Context, task durable.RunningTask[jobInput, jobState, durable.JsonValue], rt runtime) error {
					if err := appendJobOutput(ctx, rt, "$ "+task.Input.Command+"\n"); err != nil {
						return err
					}
					if err := gate.wait(ctx); err != nil {
						return err
					}
					if err := appendJobOutput(ctx, rt, "ok\n"); err != nil {
						return err
					}
					return rt.Commit(ctx, func(durable.Tx, durable.RunningTask[jobInput, jobState, durable.JsonValue]) (*durable.NextTaskState[jobState, durable.JsonValue], error) {
						return completed[jobState, durable.JsonValue](nil), nil
					})
				},
			},
			Abort: func(context.Context, durable.RunningTask[jobInput, jobState, durable.JsonValue], runtime) error {
				return nil
			},
		})
		harness, _, _ := openTasks(t, storage.NewMemoryStorage(), []durable.AnyTask{job})
		root, err := harness.Root(testContext, nil)
		if err != nil {
			t.Fatal(err)
		}
		id, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, job, jobInput{Command: "make"}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
		})
		if err != nil {
			t.Fatal(err)
		}
		harness.Resume()
		eventually(t, func() bool {
			snapshot, err := durable.Snapshot[jobOutput](testContext, harness, jobOutputDoc, id)
			return err == nil && snapshot != nil && snapshot.Chunks == 1
		})
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			_, _ = harness.WaitForTask(testContext, id)
		}()

		// observeJob: watch the producer's output until the caller-supplied observation lifetime ends.
		watch, err := harness.WatchDocErased(testContext, jobOutputDoc, id)
		if err != nil || watch == nil {
			t.Fatalf("watch: %v %v", watch, err)
		}
		var logged []string
		var sent []durable.JsonObject
		var mu sync.Mutex
		stdoutOf := func(value durable.JsonObject) string {
			if value == nil {
				return "retired"
			}
			stdout, _ := value.Value("stdout").(string)
			return stdout
		}
		logged = append(logged, stdoutOf(watch.Value()))
		watch.Start(func(_ context.Context, value durable.JsonObject, _ []durable.Op) error {
			mu.Lock()
			defer mu.Unlock()
			sent = append(sent, value) // sendCommittedFrame, then render(...)
			return nil
		}) // Serialized callbacks never overlap.
		if !reflect.DeepEqual(logged, []string{"$ make\n"}) {
			t.Fatalf("logged = %q", logged)
		}
		gate.resolve()
		<-finished                              // Caller-supplied observation lifetime; outside any commit.
		if _, err := watch.Stop(); err != nil { // Idempotent; prevents another callback from starting.
			t.Fatal(err)
		}
		// Stopped once the task finished; the retirement frame may or may not have been delivered before. The frame of
		// the producer's last output commit entered delivery before that commit returned, so Stop does not discard it;
		// its callback runs on a delivery goroutine, which WaitDeliveries joins.
		harness.(*harnessImpl).WaitDeliveries()
		mu.Lock()
		if len(sent) == 0 {
			t.Fatal("the frame of the last output commit was not delivered")
		}
		if got := stdoutOf(sent[0]); got != "$ make\nok\n" {
			t.Fatalf("rendered = %q", got)
		}
		mu.Unlock()
		after, err := harness.WatchDocErased(testContext, jobOutputDoc, id)
		if err != nil || after != nil {
			t.Fatalf("watch after the task finished: %v %v", after, err)
		}
		mustClose(t, harness)
	})
}
