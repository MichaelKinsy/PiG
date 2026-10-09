package chord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	textutil "github.com/MichaelKinsy/PiG/internal/text"
)

// FacetReplicaIdentity is the opaque identity of one underlying state replica across guarded consumer views.
type FacetReplicaIdentity replicaCore

// FacetIdentity lets an isolated callback bridge retain JavaScript listener identity without comparing Go function pointers.
func (replica *ReplicatedStateReplica) FacetIdentity() *FacetReplicaIdentity {
	return (*FacetReplicaIdentity)(replica.core)
}

// FacetService is an invocation-time view for an isolated facet whose service contract is known only at runtime. Resolve checks the same generation and facet access guards as a typed ServiceRef. The returned implementation is borrowed for that invocation; callers must not cache it across a reload.
type FacetService struct {
	resolve func() (any, error)
	remote  func() (*RemoteService, error)
}

// Resolve returns the current service implementation after checking the holder's lifetime.
func (service *FacetService) Resolve() (any, error) { return service.resolve() }

// Remote returns the real binding behind a dynamic remote service, retaining its initiating-transport capability and the holder's facet/observation guards. Native typed adapters remain the result of Resolve and are not replaced.
func (service *FacetService) Remote() (*RemoteService, error) {
	if service.remote == nil {
		return nil, errors.New("Facet service has no remote binding")
	}
	remote, err := service.remote()
	if err != nil {
		return nil, err
	}
	guarded := *remote
	previous := remote.guard
	guarded.guard = func() error {
		if _, err := service.Resolve(); err != nil {
			return err
		}
		if previous != nil {
			return previous()
		}
		return nil
	}
	return &guarded, nil
}

func (slot *serviceSlot) bindRemote(implementation any, remote *RemoteService) {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	slot.implementation, slot.remote, slot.bound = implementation, remote, true
}

// AssertFacetSettingUp applies the shared setup-only declaration guard before a foreign bridge materializes a declaration.
func AssertFacetSettingUp(env *FacetEnvironment, operation string) error {
	env.runtime.lifecycle.mu.Lock()
	defer env.runtime.lifecycle.mu.Unlock()
	return env.runtime.lifecycle.assertSettingUp(operation)
}

// AssertFacetRunning applies the existing lifecycle guard before an isolated environment creates mutable state.
func AssertFacetRunning(env *FacetEnvironment, operation string) error {
	env.runtime.lifecycle.mu.Lock()
	defer env.runtime.lifecycle.mu.Unlock()
	return env.runtime.lifecycle.assertRunning(operation)
}

// UseFacetService acquires a singleton for an isolated facet without requiring a compile-time Go service interface. Native consumers retain their registered typed adapters; otherwise remote services use their actual RemoteService facade.
func UseFacetService(env *FacetEnvironment, serviceId string, local bool) (*FacetService, error) {
	def := DefineService[any](serviceId, ServiceOptions{Local: local})
	ref, err := UseService(env, def)
	if err != nil {
		return nil, err
	}
	service := &FacetService{resolve: ref.resolve}
	if !local {
		env.ensureFacetRemoteClient(serviceId)
		service.remote = func() (*RemoteService, error) {
			if _, err := ref.resolve(); err != nil {
				return nil, err
			}
			ref.slot.mu.Lock()
			defer ref.slot.mu.Unlock()
			if ref.slot.remote == nil {
				return nil, fmt.Errorf("Service %s has no remote binding", serviceId)
			}
			return ref.slot.remote, nil
		}
	}
	return service, nil
}

// ObserveFacetService acquires a keyed service for an isolated facet. Each observed handle checks the owning facet and observation lifetime on every resolution. Remote in-host observations use the same loopback binding as external remote observations; the facet's activation waits for that loopback subscription and its snapshot handlers after its first activation callback, or after its observations start when it has none.
func ObserveFacetService(env *FacetEnvironment, serviceId string, local bool, handler func(context.Context, *FacetService) error) error {
	runtime := env.runtime
	runtime.lifecycle.mu.Lock()
	defer runtime.lifecycle.mu.Unlock()
	if err := runtime.lifecycle.assertSettingUp("observe services"); err != nil {
		return err
	}
	recordReference(&runtime.requires, serviceId, local, ServiceKeyed)
	if !local {
		env.ensureFacetRemoteClient(serviceId)
	}
	runtime.lifecycle.observations = append(runtime.lifecycle.observations, func() (func(), *task) {
		observe := func(ctx context.Context, implementation any) error {
			service := &FacetService{resolve: func() (any, error) {
				if err := runtime.lifecycle.assertServiceAccess(); err != nil {
					return nil, err
				}
				if err := env.kernel.assertServiceTargetAccess(); err != nil {
					return nil, err
				}
				if ctx.Err() != nil {
					return nil, fmt.Errorf("Keyed service %s observation is closed", serviceId)
				}
				return implementation, nil
			}}
			if !local {
				service.remote = func() (*RemoteService, error) {
					value, err := service.Resolve()
					if err != nil {
						return nil, err
					}
					remote, ok := value.(*RemoteService)
					if !ok {
						return nil, fmt.Errorf("Service %s observation has no remote binding", serviceId)
					}
					return remote, nil
				}
			}
			if _, err := service.Resolve(); err != nil {
				return err
			}
			return handler(ctx, service)
		}
		if local {
			return env.kernel.observeKeyed(serviceId, observe), nil
		}
		remoteObserve := func(ctx context.Context, implementation *RemoteService) error {
			return observe(ctx, implementation)
		}
		var stop func()
		var connecting *task
		var err error
		if external, exists := env.kernel.externalKeyed[serviceId]; exists {
			stop, err = external.Observe(serviceId, remoteObserve)
		} else {
			// The in-host loopback start is the microtask activation joins; an external source subscribes asynchronously, as upstream.
			stop, connecting, err = env.kernel.internal.observeConnecting(serviceId, remoteObserve)
		}
		if err != nil {
			env.kernel.onError(err)
			return func() {}, nil
		}
		return stop, connecting
	})
	return nil
}

// FacetServiceMember is one validated runtime-declared remote member. Exactly one of Invoke and State is present. The declaration owns neither a second provider nor a separate update stream.
type FacetServiceMember struct {
	Name   string
	Invoke func(context.Context, []json.RawMessage) (json.RawMessage, error)
	State  *FacetState
}

// FacetServiceImplementation carries a runtime method/state inventory through the existing provider's install, replace and spawn paths. Callbacks belong to the isolated loaded generation.
type FacetServiceImplementation struct {
	Members []FacetServiceMember
}

func invokeFacetMember(ctx context.Context, args []json.RawMessage, invoke func(context.Context, []json.RawMessage) (json.RawMessage, error)) (result json.RawMessage, err error) {
	defer recoverInto(&err)
	if ctx == nil {
		ctx = context.Background()
	}
	return invoke(ctx, args)
}

func classifyFacetImplementation(serviceId string, implementation *FacetServiceImplementation) (classifiedImplementation, error) {
	if implementation == nil {
		return classifiedImplementation{}, fmt.Errorf("Remote service %s implementation must be an object", serviceId)
	}
	classified := classifiedImplementation{implementation: implementation, members: map[string]instanceMember{}}
	for _, member := range implementation.Members {
		if _, exists := classified.members[member.Name]; exists {
			return classifiedImplementation{}, fmt.Errorf("Remote service %s has duplicate member %s", serviceId, member.Name)
		}
		switch {
		case member.Invoke != nil && member.State == nil:
			classified.members[member.Name] = instanceMember{kind: MemberMethod, invoke: member.Invoke}
		case member.State != nil && member.Invoke == nil:
			classified.members[member.Name] = instanceMember{kind: MemberState, state: member.State.core}
		default:
			return classifiedImplementation{}, fmt.Errorf("Remote service member %s.%s is not remotely exposable", serviceId, member.Name)
		}
		classified.names = append(classified.names, member.Name)
	}
	if len(classified.names) == 0 {
		return classifiedImplementation{}, fmt.Errorf("Remote service %s has no members", serviceId)
	}
	slices.SortFunc(classified.names, func(left, right string) int {
		return slices.Compare(textutil.UTF16Units(left), textutil.UTF16Units(right))
	})
	return classified, nil
}

// Registration occurs only during serialized facet setup. Clone the option map rather than changing the caller's configuration, and never replace a typed adapter registered by the owning service.
func (env *FacetEnvironment) ensureFacetRemoteClient(serviceId string) {
	if _, exists := env.kernel.remoteClient(serviceId); exists {
		return
	}
	clients := maps.Clone(env.kernel.remoteClients)
	if clients == nil {
		clients = map[string]func(*RemoteService) any{}
	}
	clients[serviceId] = func(service *RemoteService) any { return service }
	env.kernel.remoteClients = clients
}
