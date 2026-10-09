package chord

import (
	"context"
	"testing"
)

// packages/chord/src/types.ts:294 `replicatedState<T extends object>(initial: T): MutableReplicatedState<T>` and
// packages/chord/src/facets/host.ts:586-588: the environment's replicatedState asserts the facet is running ("setting_up" or
// "active", host.ts:77-80) and returns initialized state, so it works during setup and activation and fails with
// "Facet <id> cannot create replicated state while dead" once the host is disposed.
func TestFacetReplicatedStateFollowsTheFacetLifecycle(t *testing.T) {
	ctx := context.Background()
	var captured *FacetEnvironment
	var setupValue, activeValue int
	host := mustFacetHost(t, FacetOptions{Facets: []Facet{DefineFacet(Facet{Id: "state-owner", Setup: func(env *FacetEnvironment) error {
		captured = env
		state, err := ReplicatedState(env, &valueDocState{Value: 3})
		if err != nil {
			return err
		}
		setupValue = state.Value().Value
		return env.OnActivate(func(context.Context) error {
			state, err := ReplicatedState(env, &valueDocState{Value: 4})
			if err != nil {
				return err
			}
			activeValue = state.Value().Value
			return nil
		})
	}})}})
	if setupValue != 3 || activeValue != 4 {
		t.Fatalf("replicatedState during setup = %d, during activation = %d; want 3 and 4", setupValue, activeValue)
	}
	if err := host.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := ReplicatedState(captured, &valueDocState{Value: 5})
	if state != nil || err == nil || err.Error() != "Facet state-owner cannot create replicated state while dead" {
		t.Fatalf("replicatedState after dispose = %v, %v; want the dead-lifecycle error", state, err)
	}
}
