package coding

// Ports packages/coding-agent/src/core/model-runtime.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// registryOwnsModelBackend distinguishes lazy registry bindings from caller-owned callbacks. Metadata alone does not transfer callback or authentication ownership.
func registryOwnsModelBackend(provider ai.Provider) bool {
	switch provider.(type) {
	case nil, *catalogModelBackend, *nativeModelBackend, *nativeModelProvider, *requestOptionsProvider:
		return true
	default:
		return false
	}
}

func (runtime *ModelRuntime) bindCatalogModel(model *ai.Model) *ai.Model {
	if runtime.services.Registry().GetProvider(model.ProviderMeta.ProviderID) != nil {
		return runtime.bindNativeModel(model)
	}
	bound := new(*model)
	bound.Provider = &catalogModelBackend{runtime: runtime, model: model}
	return bound
}

type catalogModelBackend struct {
	runtime *ModelRuntime
	model   *ai.Model
	// object is set for a model object an extension gave the Session (BindModelObject).
	object *modelObjectOrigin
}

// modelObjectOrigin marks the backend of a model object an extension set that is not the registry's model. basis is the registry's model of the object's
// provider and id when it was set, nil for an id the registry did not list.
type modelObjectOrigin struct{ basis *ai.Model }

// BindModelObject gives a model object that crossed the extension wire the runtime's backend for its provider. Pi's Session takes the object an extension
// passes to setModel as its model, with the members the extension left on it, and requests use those members (agent-session.ts setModel;
// model-runtime.ts:667-689 prepareRequest); a wire object carries no stream function, so the runtime's reads the object at each request.
//
// An object that is the registry's model, member for member, is the registry's model, as the object a Pi extension takes from ctx.modelRegistry is. A
// virtual model is always the registry's, whose router the object does not carry. Any other object remembers the registry's model of its provider and id
// (none for an id the registry does not list) so a later registry change replaces it only when that model changed (Session.refreshCurrentModelFromRegistry),
// and a request sends the object and not the registry's model of its provider and id (prepareRequest).
func (runtime *ModelRuntime) BindModelObject(object *ai.Model) *ai.Model {
	if object == nil {
		return nil
	}
	listed := runtime.GetModel(providerID(object), object.ID)
	if listed != nil && (IsVirtualModel(listed) || sameModelObject(object, listed)) {
		return listed
	}
	bound := runtime.bindCatalogModel(object)
	switch backend := bound.Provider.(type) {
	case *catalogModelBackend:
		backend.object = &modelObjectOrigin{basis: listed}
	case *nativeModelBackend:
		backend.object = &modelObjectOrigin{basis: listed}
	}
	return bound
}

// modelObjectOrigin returns the origin of model when it is a model object an extension set through this runtime, nil for any other model.
func (runtime *ModelRuntime) modelObjectOrigin(model *ai.Model) *modelObjectOrigin {
	switch provider := model.Provider.(type) {
	case *catalogModelBackend:
		if provider.runtime == runtime {
			return provider.object
		}
	case *nativeModelBackend:
		if provider.runtime == runtime {
			return provider.object
		}
	}
	return nil
}

// sameModelObject reports whether two models are the same model object: their Pi model objects have the same members with the same values. The order of
// the members is not compared, because an SDK's object type may not keep it.
func sameModelObject(left, right *ai.Model) bool {
	a, err := extension.ModelObject(left)
	if err != nil || a == nil {
		return false
	}
	b, err := extension.ModelObject(right)
	if err != nil || b == nil {
		return false
	}
	return sameModelObjectJSON(a, b)
}

// sameModelObjectJSON reports whether two JSON texts hold the same value: an object's members by name whatever their order (a repeated name counts with
// its last value), an array's elements in order, a number by its float64 value and a string by its text. A text that is not JSON, or that holds a
// number float64 cannot hold, equals nothing.
func sameModelObjectJSON(left, right json.RawMessage) bool {
	var a, b modelObjectValue
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}

// modelObjectValue is one JSON value of a model object, decoded only to be compared. The zero value is null.
type modelObjectValue struct {
	kind    byte // '{', '[', '"', '0' for a number, 't' for a boolean; 0 for null
	members map[string]modelObjectValue
	items   []modelObjectValue
	text    string
	number  float64
	boolean bool
}

func (value *modelObjectValue) UnmarshalJSON(data []byte) error {
	*value = modelObjectValue{}
	if len(data) == 0 {
		return errors.New("empty JSON value")
	}
	switch data[0] {
	case 'n':
		return nil
	case '{':
		value.kind = '{'
		//portlint:allow mapkeyorder the two objects are compared member by member; no one observes the order of these copies
		return json.Unmarshal(data, &value.members)
	case '[':
		value.kind = '['
		return json.Unmarshal(data, &value.items)
	case '"':
		value.kind = '"'
		return json.Unmarshal(data, &value.text)
	case 't', 'f':
		value.kind = 't'
		return json.Unmarshal(data, &value.boolean)
	default:
		value.kind = '0'
		return json.Unmarshal(data, &value.number)
	}
}

// sameModelMetadata reports whether two models differ at most in their backend.
func sameModelMetadata(left, right *ai.Model) bool {
	a, b := *left, *right
	a.Provider, b.Provider = nil, nil
	return reflect.DeepEqual(a, b)
}

// keepsModelObject reports whether current is a model object an extension set whose registry model has not changed: refreshed, the registry's model of
// the same provider and id now, is the one the object remembers. Pi keeps the object an extension edited in place until the registry composes that
// model again, and replaces it then (agent-session.ts:3501-3513 _refreshCurrentModelFromRegistry compares object identity).
//
// pig divergence (D626): an edited copy of a registry model, which Pi replaces at the next extension provider or virtual-model (un)registration, is kept
// like the in-place edit, because the wire carries no object identity.
func (runtime *ModelRuntime) keepsModelObject(current, refreshed *ai.Model) bool {
	origin := runtime.modelObjectOrigin(current)
	return origin != nil && origin.basis != nil && sameModelMetadata(origin.basis, refreshed)
}

func (provider *catalogModelBackend) ID() string { return provider.model.ProviderMeta.ProviderID }
func (*catalogModelBackend) Close() error        { return nil }
func (provider *catalogModelBackend) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	bound := new(*provider.model)
	bound.Provider = provider
	_, backend, prepared, err := provider.runtime.prepare(ctx, bound, options)
	if err != nil {
		return nil, err
	}
	return backend.Stream(ctx, transcript, prepared)
}
