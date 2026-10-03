package experimental

// Ports packages/coding-agent/src/experimental/plugins/bundled.ts (callable isolated facet environments).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

type nodeFacetLocalService struct {
	generation *nodeFacetGeneration
	value      nodeFacetValue
}

type nodeFacetHostRequest struct {
	Op          string          `json:"op"`
	Id          string          `json:"id"`
	Property    string          `json:"property"`
	Specifier   string          `json:"specifier"`
	Method      string          `json:"method"`
	Args        json.RawMessage `json:"args"`
	Sequence    int             `json:"sequence"`
	Ops         []chord.Op      `json:"ops"`
	Origin      string          `json:"origin"`
	Cancellable bool            `json:"cancellable"`
	Context     nodeFacetValue  `json:"context"`
	Cancelled   bool            `json:"cancelled"`
}

func dispatchNodeFacetHost(ctx context.Context, generation *nodeFacetGeneration, _ string, raw json.RawMessage) (json.RawMessage, error) {
	generation.mu.Lock()
	closed := generation.closed
	generation.mu.Unlock()
	if closed {
		return nil, errors.New("Facet generation is closed")
	}
	var request nodeFacetHostRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	var value nodeFacetValue
	var err error
	switch request.Op {
	case "resolveExternal":
		if generation.external == nil {
			return nil, errors.New("Facet external resolver is unavailable")
		}
		value, defined, err := generation.external(request.Specifier)
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Defined bool   `json:"defined"`
			Value   string `json:"value"`
		}{defined, value})
	case "context":
		generation.mu.Lock()
		base := generation.contextOrigins[request.Origin]
		if request.Origin != "" && base == nil {
			generation.mu.Unlock()
			return nil, errors.New("Facet Context origin is released")
		}
		if base == nil {
			base = context.Background()
		}
		contextValue := context.WithValue(base, nodeFacetContextKey{}, nodeFacetContextOrigin{generation, request.Id})
		var cancel context.CancelFunc
		if request.Cancellable {
			contextValue, cancel = context.WithCancel(contextValue)
		}
		if old := generation.contextCancel[request.Id]; old != nil {
			old()
		}
		generation.contexts[request.Id], generation.contextCancel[request.Id] = contextValue, cancel
		generation.mu.Unlock()
		if request.Cancelled && cancel != nil {
			cancel()
		}
		value = nodeFacetValue{Kind: "undefined"}
	case "contextCancel":
		generation.mu.Lock()
		cancel := generation.contextCancel[request.Id]
		generation.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		value = nodeFacetValue{Kind: "undefined"}
	case "environment":
		value, err = generation.environmentCall(ctx, request)
	case "state":
		generation.mu.Lock()
		state := generation.states[request.Id]
		generation.mu.Unlock()
		if state == nil {
			return nil, errors.New("Facet state reference is released")
		}
		generation.mu.Lock()
		deliveryContext := generation.contexts[request.Context.Id]
		generation.mu.Unlock()
		if deliveryContext == nil {
			return nil, errors.New("Facet state delivery Context is released")
		}
		err = state.apply(deliveryContext, request.Sequence, request.Ops)
		value = nodeFacetValue{Kind: "undefined"}
	case "await":
		owned, resolveErr := generation.hostValue(request.Id)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if owned.wait == nil {
			return nil, errors.New("Facet host reference is not a pending operation")
		}
		value, err = owned.wait(ctx)
	case "get", "call":
		owned, resolveErr := generation.hostValue(request.Id)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if request.Op == "get" {
			if owned.get == nil {
				value = nodeFacetValue{Kind: "undefined"}
			} else {
				value, err = owned.get(ctx, request.Property)
			}
		} else {
			if owned.call == nil {
				return nil, errors.New("Facet host reference is not callable")
			}
			var args []nodeFacetValue
			if err := json.Unmarshal(request.Args, &args); err != nil {
				return nil, err
			}
			value, err = owned.call(ctx, args)
		}
	default:
		return nil, fmt.Errorf("Unknown facet host operation: %s", request.Op)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func (generation *nodeFacetGeneration) environmentCall(ctx context.Context, request nodeFacetHostRequest) (nodeFacetValue, error) {
	generation.mu.Lock()
	env := generation.environments[request.Id]
	generation.mu.Unlock()
	if env == nil {
		return nodeFacetValue{}, errors.New("Facet environment is released")
	}
	var args struct {
		Service struct {
			Id    string `json:"id"`
			Local bool   `json:"local"`
		} `json:"service"`
		Callback       nodeFacetValue `json:"callback"`
		Handler        nodeFacetValue `json:"handler"`
		Implementation nodeFacetValue `json:"implementation"`
		Operation      string         `json:"operation"`
	}
	if err := json.Unmarshal(request.Args, &args); err != nil {
		return nodeFacetValue{}, err
	}
	undefined := nodeFacetValue{Kind: "undefined"}
	switch request.Method {
	case "use":
		if err := chord.AssertFacetSettingUp(env, "acquire services"); err != nil {
			return nodeFacetValue{}, err
		}
		generation.mu.Lock()
		cached, found := generation.environmentViews[request.Id][args.Service.Id]
		generation.mu.Unlock()
		if found {
			return cached, nil
		}
		ref, err := chord.UseFacetService(env, args.Service.Id, args.Service.Local)
		if err != nil {
			return nodeFacetValue{}, err
		}
		value := generation.serviceValue(args.Service.Id, args.Service.Local, ref)
		generation.mu.Lock()
		if generation.environmentViews[request.Id] == nil {
			generation.environmentViews[request.Id] = map[string]nodeFacetValue{}
		}
		generation.environmentViews[request.Id][args.Service.Id] = value
		generation.mu.Unlock()
		return value, nil
	case "provide":
		if err := chord.AssertFacetSettingUp(env, "provide services"); err != nil {
			return nodeFacetValue{}, err
		}
		implementation, err := generation.serviceImplementation(ctx, env, args.Service.Id, args.Service.Local, args.Implementation)
		if err != nil {
			return nodeFacetValue{}, err
		}
		definition := chord.DefineServiceWithOptions[any](args.Service.Id, chord.ServiceOptions{Local: args.Service.Local})
		return undefined, chord.ProvideService(env, definition, implementation)
	case "provideMany":
		definition := chord.DefineServiceWithOptions[any](args.Service.Id, chord.ServiceOptions{Local: args.Service.Local})
		spawner, err := chord.ProvideMany(env, definition)
		if err != nil {
			return nodeFacetValue{}, err
		}
		spawn := generation.ownHost(nodeFacetHostValue{call: func(ctx context.Context, values []nodeFacetValue) (nodeFacetValue, error) {
			if len(values) != 2 {
				return nodeFacetValue{}, errors.New("Facet spawn requires key and implementation")
			}
			var key string
			if err := generation.decodeJSON(ctx, values[0], &key); err != nil {
				return nodeFacetValue{}, err
			}
			implementation, err := generation.serviceImplementation(ctx, env, args.Service.Id, args.Service.Local, values[1])
			if err != nil {
				return nodeFacetValue{}, err
			}
			closeInstance, err := spawner.Spawn(key, implementation)
			if err != nil {
				return nodeFacetValue{}, err
			}
			return generation.disposalValue(closeInstance), nil
		}})
		return generation.objectValue(map[string]nodeFacetValue{"spawn": spawn}), nil
	case "observe":
		// upstream: packages/chord/src/services/instances.ts:#start applies the handler synchronously and leaves its returned Promise unawaited, reporting only a rejection that precedes cancellation.
		return undefined, chord.ObserveFacetService(env, args.Service.Id, args.Service.Local, func(ctx context.Context, ref *chord.FacetService) error {
			handlerArgs := []nodeFacetValue{generation.serviceValue(args.Service.Id, args.Service.Local, ref), generation.contextValue(ctx)}
			if args.Handler.Kind != "node" {
				_, err := generation.invoke(ctx, true, args.Handler, nil, handlerArgs)
				return err
			}
			result, err := generation.invokeObservation(ctx, args.Handler, handlerArgs)
			if err != nil || result.Kind != "node" {
				return err
			}
			// Node returns Promise.resolve(result), which assimilates a thenable as well as a Promise (instances.ts:127-136), under a private reference the observation owns until it settles or is cancelled.
			chord.ContinueObservation(ctx, func() error {
				err := generation.request(ctx, false, map[string]any{"op": "await", "id": result.Id, "observation": true}, nil)
				if ctx.Err() != nil {
					generation.releaseNodeValues(result.Id)
				}
				return err
			})
			return nil
		})
	case "own", "onActivate", "onDeactivate":
		callback := func(ctx context.Context) error {
			_, err := generation.invoke(ctx, false, args.Callback, nil, nil)
			return err
		}
		switch request.Method {
		case "onActivate":
			return undefined, env.OnActivate(callback)
		case "onDeactivate":
			return undefined, env.OnDeactivate(callback)
		default:
			return undefined, env.Own(callback)
		}
	case "assertRunning":
		return undefined, chord.AssertFacetRunning(env, args.Operation)
	default:
		return nodeFacetValue{}, fmt.Errorf("Unknown facet environment operation: %s", request.Method)
	}
}

func (generation *nodeFacetGeneration) decodeJSON(ctx context.Context, value nodeFacetValue, target any) error {
	raw, err := generation.jsonValue(ctx, value)
	if err != nil {
		return err
	}
	if raw == nil {
		return errors.New("Facet argument is undefined")
	}
	return json.Unmarshal(raw, target)
}

func (generation *nodeFacetGeneration) objectValue(properties map[string]nodeFacetValue) nodeFacetValue {
	return generation.ownHost(nodeFacetHostValue{get: func(_ context.Context, name string) (nodeFacetValue, error) {
		if value, ok := properties[name]; ok {
			return value, nil
		}
		return nodeFacetValue{Kind: "undefined"}, nil
	}})
}

func (generation *nodeFacetGeneration) disposalValue(dispose func()) nodeFacetValue {
	var once sync.Once
	return generation.ownHost(nodeFacetHostValue{call: func(context.Context, []nodeFacetValue) (nodeFacetValue, error) {
		once.Do(dispose)
		return nodeFacetValue{Kind: "undefined"}, nil
	}})
}

func (generation *nodeFacetGeneration) serviceImplementation(ctx context.Context, env *chord.FacetEnvironment, id string, local bool, value nodeFacetValue) (any, error) {
	if local {
		return &nodeFacetLocalService{generation: generation, value: value}, nil
	}
	var members []struct {
		Name  string         `json:"name"`
		Kind  string         `json:"kind"`
		Value nodeFacetValue `json:"value"`
	}
	if err := generation.request(ctx, true, map[string]any{"op": "describeService", "id": value.Id, "serviceId": id}, &members); err != nil {
		return nil, err
	}
	implementation := &chord.FacetServiceImplementation{}
	for _, member := range members {
		switch member.Kind {
		case "method":
			implementation.Members = append(implementation.Members, chord.FacetServiceMember{Name: member.Name, Invoke: func(ctx context.Context, args []json.RawMessage) (json.RawMessage, error) {
				values := make([]nodeFacetValue, 0, len(args)+1)
				for _, arg := range args {
					values = append(values, nodeFacetValue{Kind: "json", Value: arg})
				}
				values = append(values, generation.contextValue(ctx))
				return generation.invokeJSON(ctx, member.Value, &value, values)
			}})
		case "state":
			generation.mu.Lock()
			generation.nextId++
			target := strconv.FormatUint(generation.nextId, 10)
			generation.mu.Unlock()
			var snapshot struct {
				Sequence    int            `json:"sequence"`
				Value       any            `json:"value"`
				Unsubscribe nodeFacetValue `json:"unsubscribe"`
			}
			pending := &nodeFacetState{}
			generation.mu.Lock()
			generation.states[target] = pending
			generation.mu.Unlock()
			if err := generation.request(ctx, true, map[string]any{"op": "watchState", "id": member.Value.Id, "target": target}, &snapshot); err != nil {
				return nil, err
			}
			state, err := pending.install(snapshot.Value, snapshot.Sequence)
			if err != nil {
				return nil, err
			}
			if err := env.Own(func(ctx context.Context) error {
				_, err := generation.invoke(ctx, true, snapshot.Unsubscribe, nil, nil)
				generation.mu.Lock()
				delete(generation.states, target)
				generation.mu.Unlock()
				return err
			}); err != nil {
				return nil, err
			}
			implementation.Members = append(implementation.Members, chord.FacetServiceMember{Name: member.Name, State: state})
		default:
			return nil, fmt.Errorf("Invalid facet service member kind: %s", member.Kind)
		}
	}
	return implementation, nil
}

func (generation *nodeFacetGeneration) serviceValue(id string, local bool, ref *chord.FacetService) nodeFacetValue {
	members := map[string]nodeFacetValue{}
	var mu sync.Mutex
	return generation.ownHost(nodeFacetHostValue{get: func(ctx context.Context, name string) (nodeFacetValue, error) {
		if _, err := ref.Resolve(); err != nil {
			return nodeFacetValue{}, err
		}
		mu.Lock()
		value, exists := members[name]
		mu.Unlock()
		if exists {
			return value, nil
		}
		var err error
		if local {
			value, err = generation.localMember(ctx, id, ref, name)
		} else {
			value = generation.remoteMember(ref, name)
		}
		if err != nil {
			return nodeFacetValue{}, err
		}
		if !local || value.Callable {
			mu.Lock()
			members[name] = value
			mu.Unlock()
		}
		return value, nil
	}})
}

func (generation *nodeFacetGeneration) remoteMember(ref *chord.FacetService, name string) nodeFacetValue {
	var subscribeOnce sync.Once
	var subscribe nodeFacetValue
	return generation.ownHost(nodeFacetHostValue{tag: "RemoteServiceMember", asynchronous: true, call: func(ctx context.Context, args []nodeFacetValue) (nodeFacetValue, error) {
		remote, err := ref.Remote()
		if err != nil {
			return nodeFacetValue{}, err
		}
		if len(args) == 0 {
			return nodeFacetValue{}, errors.New("Remote service method requires a trailing Context")
		}
		values := make([]any, 0, len(args)-1)
		for _, arg := range args[:len(args)-1] {
			raw, err := generation.jsonValue(ctx, arg)
			if err != nil {
				return nodeFacetValue{}, err
			}
			if raw == nil {
				return nodeFacetValue{}, errors.New("Remote service argument is undefined")
			}
			values = append(values, raw)
		}
		operationContext, release, err := generation.operationContext(ctx, args[len(args)-1])
		if err != nil {
			return nodeFacetValue{}, err
		}
		defer release()
		operation, err := remote.BeginCall(operationContext, name, values...)
		if err != nil {
			return nodeFacetValue{}, err
		}
		result, err := operation.Wait(context.Background())
		if err != nil {
			return nodeFacetValue{}, err
		}
		if result == nil {
			return nodeFacetValue{Kind: "undefined"}, nil
		}
		return nodeFacetValue{Kind: "json", Value: result}, nil
	}, get: func(ctx context.Context, property string) (nodeFacetValue, error) {
		remote, err := ref.Remote()
		if err != nil {
			return nodeFacetValue{}, err
		}
		switch property {
		case "value":
			state, err := remote.State(name)
			if err != nil {
				return nodeFacetValue{}, err
			}
			value, hydrated, err := state.Load()
			if err != nil {
				return nodeFacetValue{}, err
			}
			if !hydrated {
				return nodeFacetValue{Kind: "undefined"}, nil
			}
			return nodeFacetJSON(value)
		case "subscribe":
			subscribeOnce.Do(func() {
				subscribe = generation.ownHost(nodeFacetHostValue{call: func(ctx context.Context, args []nodeFacetValue) (nodeFacetValue, error) {
					remote, err := ref.Remote()
					if err != nil {
						return nodeFacetValue{}, err
					}
					state, err := remote.State(name)
					if err != nil {
						return nodeFacetValue{}, err
					}
					if len(args) != 1 {
						return nodeFacetValue{}, errors.New("State subscribe requires a callback")
					}
					return generation.subscribeReplica(state, args[0])
				}})
			})
			return subscribe, nil
		default:
			return nodeFacetValue{Kind: "undefined"}, nil
		}
	}})
}

func (generation *nodeFacetGeneration) subscribeReplica(state *chord.ReplicatedStateReplica, callback nodeFacetValue) (nodeFacetValue, error) {
	key := nodeFacetReplicaKey{state.FacetIdentity(), callback.Kind + ":" + callback.Id}
	deliver := func(value chord.JsonValue, ctx context.Context, delivery chord.ReplicatedStateDelivery) error {
		encoded, err := nodeFacetJSON(value)
		if err != nil {
			return err
		}
		metadata, err := nodeFacetJSON(map[string]any{"kind": delivery.Kind, "sequence": delivery.Sequence})
		if err != nil {
			return err
		}
		_, err = generation.invoke(context.Background(), true, callback, nil, []nodeFacetValue{encoded, generation.contextValue(ctx), metadata})
		return err
	}
	generation.mu.Lock()
	existing := generation.stateSubscriptions[key]
	generation.mu.Unlock()
	if existing != nil {
		existing.mu.Lock()
		value, sequence, hydrated := existing.latest, existing.sequence, existing.hydrated
		existing.mu.Unlock()
		if hydrated {
			if err := deliver(value, context.Background(), chord.ReplicatedStateDelivery{Kind: "hydrate", Sequence: sequence}); err != nil {
				return nodeFacetValue{}, err
			}
		}
		return existing.value, nil
	}
	subscription := &nodeFacetReplicaSubscription{}
	subscription.value = generation.disposalValue(func() {
		subscription.mu.Lock()
		subscription.closed = true
		remove := subscription.remove
		subscription.mu.Unlock()
		if remove != nil {
			remove()
		}
		generation.mu.Lock()
		if generation.stateSubscriptions[key] == subscription {
			delete(generation.stateSubscriptions, key)
		}
		generation.mu.Unlock()
	})
	generation.mu.Lock()
	generation.stateSubscriptions[key] = subscription
	generation.mu.Unlock()
	remove, err := state.Subscribe(func(value chord.JsonValue, ctx context.Context, delivery chord.ReplicatedStateDelivery) error {
		subscription.mu.Lock()
		subscription.latest, subscription.sequence, subscription.hydrated = value, delivery.Sequence, true
		subscription.mu.Unlock()
		return deliver(value, ctx, delivery)
	})
	if err != nil {
		generation.mu.Lock()
		delete(generation.stateSubscriptions, key)
		generation.mu.Unlock()
		return nodeFacetValue{}, err
	}
	subscription.mu.Lock()
	subscription.remove = remove
	closed := subscription.closed
	subscription.mu.Unlock()
	if closed {
		remove()
	}
	return subscription.value, nil
}

func (generation *nodeFacetGeneration) localMember(ctx context.Context, id string, ref *chord.FacetService, name string) (nodeFacetValue, error) {
	implementation, err := ref.Resolve()
	if err != nil {
		return nodeFacetValue{}, err
	}
	if remote, ok := implementation.(*nodeFacetLocalService); ok {
		var value nodeFacetValue
		if err := remote.generation.request(ctx, true, map[string]any{"op": "get", "id": remote.value.Id, "property": name}, &value); err != nil {
			return nodeFacetValue{}, err
		}
		if value.Callable {
			return generation.ownHost(nodeFacetHostValue{asynchronous: value.Asynchronous, call: func(ctx context.Context, args []nodeFacetValue) (nodeFacetValue, error) {
				current, err := ref.Resolve()
				if err != nil {
					return nodeFacetValue{}, err
				}
				target := current.(*nodeFacetLocalService)
				var callback nodeFacetValue
				if err := target.generation.request(ctx, true, map[string]any{"op": "get", "id": target.value.Id, "property": name}, &callback); err != nil {
					return nodeFacetValue{}, err
				}
				translated := make([]nodeFacetValue, len(args))
				for i, arg := range args {
					value, err := target.generation.adoptValue(ctx, generation, arg)
					if err != nil {
						return nodeFacetValue{}, err
					}
					translated[i] = value
				}
				result, err := target.generation.invoke(ctx, !callback.Asynchronous, callback, &target.value, translated)
				if err != nil {
					return nodeFacetValue{}, err
				}
				return generation.adoptValue(ctx, target.generation, result)
			}}), nil
		}
		return generation.adoptValue(ctx, remote.generation, value)
	}
	switch id {
	case services.SlashCommandsDefinition.Id():
		return generation.slashMember(ref, name)
	case services.PresentationUIDefinition.Id():
		return generation.presentationMember(ref, name)
	default:
		return nodeFacetValue{}, fmt.Errorf("Local service %s has no isolated Go adapter", id)
	}
}

func (generation *nodeFacetGeneration) slashMember(ref *chord.FacetService, name string) (nodeFacetValue, error) {
	switch name {
	case "register", "replace", "list", "subscribe":
	default:
		return nodeFacetValue{Kind: "undefined"}, nil
	}
	return generation.ownHost(nodeFacetHostValue{call: func(ctx context.Context, args []nodeFacetValue) (nodeFacetValue, error) {
		current, err := ref.Resolve()
		if err != nil {
			return nodeFacetValue{}, err
		}
		registry, ok := current.(services.SlashCommands)
		if !ok {
			return nodeFacetValue{}, errors.New("SlashCommands contract mismatch")
		}
		switch name {
		case "register", "replace":
			if len(args) != 1 {
				return nodeFacetValue{}, errors.New("Slash registration requires one contribution")
			}
			command, err := generation.decodeCommand(ctx, args[0])
			if err != nil {
				return nodeFacetValue{}, err
			}
			var remove func()
			if name == "register" {
				remove, err = registry.Register(command)
			} else {
				remove, err = registry.Replace(command)
			}
			if err != nil {
				return nodeFacetValue{}, err
			}
			return generation.disposalValue(remove), nil
		case "list":
			return generation.commandList(ctx, registry.List())
		case "subscribe":
			ctx = context.WithoutCancel(ctx)
			if len(args) != 1 || !args[0].Callable {
				return nodeFacetValue{}, errors.New("Slash subscribe requires one callback")
			}
			if !reflect.ValueOf(registry).Comparable() {
				return nodeFacetValue{}, errors.New("Native SlashCommands must expose a stable instance identity")
			}
			key := nodeFacetSlashKey{registry, args[0].Kind + ":" + args[0].Id}
			publish := func(commands []services.SlashCommandContribution) {
				value, err := generation.commandList(ctx, commands)
				if err != nil {
					panic(err)
				}
				if _, err := generation.invoke(ctx, true, args[0], nil, []nodeFacetValue{value}); err != nil {
					panic(err)
				}
			}
			generation.mu.Lock()
			existing := generation.slashSubscriptions[key]
			generation.mu.Unlock()
			if existing != nil {
				publish(registry.List())
				return existing.value, nil
			}
			subscription := &nodeFacetSlashSubscription{}
			subscription.value = generation.disposalValue(func() {
				subscription.once.Do(func() {
					subscription.mu.Lock()
					subscription.closed = true
					remove := subscription.remove
					subscription.mu.Unlock()
					if remove != nil {
						remove()
					}
					generation.mu.Lock()
					if generation.slashSubscriptions[key] == subscription {
						delete(generation.slashSubscriptions, key)
					}
					generation.mu.Unlock()
				})
			})
			generation.mu.Lock()
			generation.slashSubscriptions[key] = subscription
			generation.mu.Unlock()
			remove := registry.Subscribe(publish)
			subscription.mu.Lock()
			subscription.remove = remove
			closed := subscription.closed
			subscription.mu.Unlock()
			if closed {
				remove()
			}
			return subscription.value, nil
		default:
			panic("unreachable slash operation")
		}
	}}), nil
}

func (generation *nodeFacetGeneration) property(ctx context.Context, object nodeFacetValue, name string) (nodeFacetValue, error) {
	if object.Kind != "node" {
		return nodeFacetValue{}, errors.New("Facet contribution must be an object")
	}
	var value nodeFacetValue
	err := generation.request(ctx, true, map[string]any{"op": "get", "id": object.Id, "property": name}, &value)
	return value, err
}

func (generation *nodeFacetGeneration) decodeCommand(ctx context.Context, object nodeFacetValue) (services.SlashCommandContribution, error) {
	command := services.SlashCommandContribution{}
	name, err := generation.property(ctx, object, "name")
	if err != nil {
		return command, err
	}
	if err := generation.decodeJSON(ctx, name, &command.Name); err != nil {
		return command, err
	}
	for _, field := range []struct {
		name   string
		target **string
	}{{"description", &command.Description}, {"argumentHint", &command.ArgumentHint}} {
		value, err := generation.property(ctx, object, field.name)
		if err != nil {
			return command, err
		}
		if value.Kind != "undefined" {
			if err := generation.decodeJSON(ctx, value, field.target); err != nil {
				return command, err
			}
		}
	}
	run, err := generation.property(ctx, object, "run")
	if err != nil {
		return command, err
	}
	command.Run = func(ctx context.Context, args string) (services.SlashCommandRunResult, error) {
		argument, err := nodeFacetJSON(args)
		if err != nil {
			return nil, err
		}
		raw, err := generation.invokeJSON(ctx, run, &object, []nodeFacetValue{argument, generation.contextValue(ctx)})
		if err != nil || raw == nil {
			return nil, err
		}
		var shape map[string]json.RawMessage
		if err := json.Unmarshal(raw, &shape); err != nil {
			return nil, err
		}
		if _, queue := shape["entryId"]; queue {
			var response services.AgentQueueResponse
			err := json.Unmarshal(raw, &response)
			return response, err
		}
		var response services.AgentOperationResponse
		err = json.Unmarshal(raw, &response)
		return response, err
	}
	completion, err := generation.property(ctx, object, "getArgumentCompletions")
	if err != nil {
		return command, err
	}
	if completion.Kind != "undefined" {
		command.GetArgumentCompletions = func(prefix string) ([]services.SlashCommandCompletion, error) {
			argument, err := nodeFacetJSON(prefix)
			if err != nil {
				return nil, err
			}
			raw, err := generation.invokeJSON(context.Background(), completion, &object, []nodeFacetValue{argument})
			if err != nil {
				return nil, err
			}
			var values []struct {
				Value       string  `json:"value"`
				Label       string  `json:"label"`
				Description *string `json:"description"`
			}
			if raw == nil {
				return nil, errors.New("Facet completion result is undefined")
			}
			if err := json.Unmarshal(raw, &values); err != nil {
				return nil, err
			}
			if values == nil {
				return nil, nil
			}
			out := make([]services.SlashCommandCompletion, len(values))
			for i, value := range values {
				out[i] = services.SlashCommandCompletion{Value: value.Value, Label: value.Label, Description: value.Description}
			}
			return out, nil
		}
	}
	return command.WithOrigin(services.SlashCommandOrigin{Owner: generation.directory, Handle: object.Id})
}

func (generation *nodeFacetGeneration) commandList(ctx context.Context, commands []services.SlashCommandContribution) (nodeFacetValue, error) {
	values := make([]nodeFacetValue, len(commands))
	for index, command := range commands {
		identity := command.RegistrationIdentity()
		if identity == nil {
			return nodeFacetValue{}, errors.New("Slash command has no registration identity")
		}
		generation.mu.Lock()
		existing, found := generation.commands[identity]
		generation.mu.Unlock()
		if found {
			values[index] = existing
			continue
		}
		properties := map[string]nodeFacetValue{}
		name, err := nodeFacetJSON(command.Name)
		if err != nil {
			return nodeFacetValue{}, err
		}
		properties["name"] = name
		for _, field := range []struct {
			name  string
			value *string
		}{{"description", command.Description}, {"argumentHint", command.ArgumentHint}} {
			if field.value != nil {
				encoded, err := nodeFacetJSON(*field.value)
				if err != nil {
					return nodeFacetValue{}, err
				}
				properties[field.name] = encoded
			}
		}
		if origin, ok := command.Origin(); ok && origin.Owner == generation.directory {
			object := nodeFacetValue{Kind: "node", Id: origin.Handle}
			for _, field := range []string{"run", "getArgumentCompletions"} {
				value, err := generation.property(ctx, object, field)
				if err != nil {
					return nodeFacetValue{}, err
				}
				properties[field] = value
			}
		} else {
			properties["run"] = generation.ownHost(nodeFacetHostValue{asynchronous: true, call: func(ctx context.Context, args []nodeFacetValue) (nodeFacetValue, error) {
				if len(args) != 2 {
					return nodeFacetValue{}, errors.New("Slash run requires arguments and Context")
				}
				var text string
				if err := generation.decodeJSON(ctx, args[0], &text); err != nil {
					return nodeFacetValue{}, err
				}
				operationContext, release, err := generation.operationContext(ctx, args[1])
				if err != nil {
					return nodeFacetValue{}, err
				}
				defer release()
				result, err := command.Run(operationContext, text)
				if err != nil {
					return nodeFacetValue{}, err
				}
				if result == nil {
					return nodeFacetValue{Kind: "undefined"}, nil
				}
				return nodeFacetJSON(result)
			}})
			if command.GetArgumentCompletions != nil {
				properties["getArgumentCompletions"] = generation.ownHost(nodeFacetHostValue{asynchronous: true, call: func(ctx context.Context, args []nodeFacetValue) (nodeFacetValue, error) {
					if len(args) != 1 {
						return nodeFacetValue{}, errors.New("Slash completions require a prefix")
					}
					var prefix string
					if err := generation.decodeJSON(ctx, args[0], &prefix); err != nil {
						return nodeFacetValue{}, err
					}
					items, err := command.GetArgumentCompletions(prefix)
					if err != nil {
						return nodeFacetValue{}, err
					}
					if items == nil {
						return nodeFacetJSON(nil)
					}
					values := make([]map[string]any, len(items))
					for i, item := range items {
						values[i] = map[string]any{"value": item.Value, "label": item.Label}
						if item.Description != nil {
							values[i]["description"] = *item.Description
						}
					}
					return nodeFacetJSON(values)
				}})
			}
		}
		var encoded nodeFacetValue
		if err := generation.request(ctx, true, map[string]any{"op": "command", "properties": properties}, &encoded); err != nil {
			return nodeFacetValue{}, err
		}
		generation.mu.Lock()
		generation.commands[identity] = encoded
		generation.mu.Unlock()
		values[index] = encoded
	}
	var result nodeFacetValue
	err := generation.request(ctx, true, map[string]any{"op": "array", "values": values}, &result)
	return result, err
}

func (generation *nodeFacetGeneration) presentationMember(ref *chord.FacetService, name string) (nodeFacetValue, error) {
	if name != "select" && name != "showStatus" {
		return nodeFacetValue{Kind: "undefined"}, nil
	}
	return generation.ownHost(nodeFacetHostValue{asynchronous: name == "select", call: func(ctx context.Context, args []nodeFacetValue) (nodeFacetValue, error) {
		current, err := ref.Resolve()
		if err != nil {
			return nodeFacetValue{}, err
		}
		ui, ok := current.(services.PresentationUI)
		if !ok {
			return nodeFacetValue{}, errors.New("PresentationUI contract mismatch")
		}
		if len(args) == 0 {
			return nodeFacetValue{}, errors.New("PresentationUI requires a message")
		}
		var text string
		if err := generation.decodeJSON(ctx, args[0], &text); err != nil {
			return nodeFacetValue{}, err
		}
		if name == "showStatus" {
			if len(args) != 2 {
				return nodeFacetValue{}, errors.New("PresentationUI.showStatus requires message and Context")
			}
			operationContext, release, err := generation.operationContext(ctx, args[1])
			if err != nil {
				return nodeFacetValue{}, err
			}
			defer release()
			return nodeFacetValue{Kind: "undefined"}, ui.ShowStatus(operationContext, text)
		}
		if len(args) != 4 {
			return nodeFacetValue{}, errors.New("PresentationUI.select requires title, items, selection and Context")
		}
		var items []struct {
			Value       string  `json:"value"`
			Label       string  `json:"label"`
			Description *string `json:"description"`
		}
		if err := generation.decodeJSON(ctx, args[1], &items); err != nil {
			return nodeFacetValue{}, err
		}
		choices := make([]services.PresentationSelectItem, len(items))
		for i, item := range items {
			choices[i] = services.PresentationSelectItem{Value: item.Value, Label: item.Label, Description: item.Description}
		}
		var selected *string
		if args[2].Kind != "undefined" {
			if err := generation.decodeJSON(ctx, args[2], &selected); err != nil {
				return nodeFacetValue{}, err
			}
		}
		operationContext, release, err := generation.operationContext(ctx, args[3])
		if err != nil {
			return nodeFacetValue{}, err
		}
		defer release()
		value, err := ui.Select(operationContext, text, choices, selected)
		if err != nil {
			return nodeFacetValue{}, err
		}
		if value == nil {
			return nodeFacetValue{Kind: "undefined"}, nil
		}
		return nodeFacetJSON(*value)
	}}), nil
}
