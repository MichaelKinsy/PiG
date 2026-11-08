// Package login is the provider login plugin (`p2.provider-login`): signing in to a model provider with its own flow (an API
// key typed in, or OAuth: a browser URL, a device code, a pasted code) over the credentials the models plugin's configuration
// keeps in auth.json. One login runs at a time; it belongs to the owners (all of them see its prompts and notices).
package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/login/contract"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// attempt is the login in progress or just finished.
type attempt struct {
	id, provider string
	authType     ai.AuthType
	status       string // working, success, cancelled, error
	prompt       *delta.JsonObject
	promptID     string
	notices      []*delta.JsonObject
	message      string
	cancel       context.CancelFunc
	answers      chan string
}

// manager holds the one login of a server and tells whoever watches it.
type manager struct {
	mu        sync.Mutex
	current   *attempt
	seq       int
	noticeSeq int
	listeners map[int]func()
	next      int
}

func (m *manager) subscribe(changed func()) func() {
	m.mu.Lock()
	if m.listeners == nil {
		m.listeners = map[int]func(){}
	}
	m.next++
	id := m.next
	m.listeners[id] = changed
	m.mu.Unlock()
	return func() { m.mu.Lock(); delete(m.listeners, id); m.mu.Unlock() }
}

func (m *manager) changed() {
	m.mu.Lock()
	listeners := make([]func(), 0, len(m.listeners))
	for _, listener := range m.listeners {
		listeners = append(listeners, listener)
	}
	m.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}

// login is the state's `login`: null without one.
func (m *manager) login() delta.JsonValue {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.current
	if a == nil {
		return nil
	}
	notices := make([]any, len(a.notices))
	for i, n := range a.notices {
		notices[i] = n
	}
	out := delta.JsonObjectOf("attemptId", a.id, "providerId", a.provider, "authType", string(a.authType), "status", a.status, "notices", notices)
	if a.prompt != nil {
		out.Set("prompt", a.prompt)
	} else {
		out.Set("prompt", nil)
	}
	if a.message != "" {
		out.Set("message", a.message)
	}
	return out
}

// jsonOf is a value as a chord JSON value, through its JSON text.
func jsonOf(value any) (*delta.JsonObject, error) {
	text, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoded, err := chordjson.Decode(text)
	if err != nil {
		return nil, err
	}
	object, _ := decoded.(*delta.JsonObject)
	if object == nil {
		return nil, errors.New("not an object")
	}
	return object, nil
}

func (m *manager) set(a *attempt, mutate func()) {
	m.mu.Lock()
	mutate()
	m.mu.Unlock()
	m.changed()
}

// start runs provider's login of authType.
func (m *manager) start(resources modelscontract.Resources, provider string, authType ai.AuthType) (string, error) {
	models := resources.Models()
	if models.GetProvider(provider) == nil {
		return "", fmt.Errorf("Unknown provider: %s", provider)
	}
	m.mu.Lock()
	if m.current != nil && m.current.status == "working" {
		m.mu.Unlock()
		return "", errors.New("A login is already in progress")
	}
	m.seq++
	ctx, cancel := context.WithCancel(context.Background())
	a := &attempt{id: fmt.Sprintf("login-%d", m.seq), provider: provider, authType: authType, status: "working", cancel: cancel, answers: make(chan string, 1)}
	m.current = a
	m.mu.Unlock()
	m.changed()
	interaction := ai.AuthInteraction{
		Prompt: func(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
			object, err := jsonOf(prompt)
			if err != nil {
				return "", err
			}
			select { // an answer left over from a question that closed
			case <-a.answers:
			default:
			}
			m.mu.Lock()
			m.noticeSeq++
			id := fmt.Sprintf("prompt-%d", m.noticeSeq)
			object.Set("id", id)
			a.prompt, a.promptID = object, id
			m.mu.Unlock()
			m.changed()
			defer m.set(a, func() { a.prompt, a.promptID = nil, "" })
			select {
			case value := <-a.answers:
				return value, nil
			case <-ctx.Done():
				return "", context.Cause(ctx)
			}
		},
		Notify: func(event ai.AuthEvent) {
			object, err := jsonOf(event)
			if err != nil {
				return
			}
			m.set(a, func() {
				m.noticeSeq++
				a.notices = append(a.notices, delta.JsonObjectOf("id", float64(m.noticeSeq), "event", object))
			})
		},
	}
	go func() {
		_, err := models.Login(ctx, provider, authType, interaction)
		m.set(a, func() {
			switch {
			case ctx.Err() != nil:
				a.status = "cancelled"
			case err != nil:
				a.status, a.message = "error", err.Error()
			default:
				a.status = "success"
			}
			a.prompt, a.promptID = nil, ""
		})
		if err == nil {
			resources.Publish()
		}
	}()
	return a.id, nil
}

func (m *manager) answer(attemptID, promptID, value string) error {
	m.mu.Lock()
	a := m.current
	ok := a != nil && a.id == attemptID && a.promptID != "" && a.promptID == promptID
	m.mu.Unlock()
	if !ok {
		return errors.New("That question is no longer asked")
	}
	select {
	case a.answers <- value:
		return nil
	default:
		return errors.New("That question is no longer asked")
	}
}

func (m *manager) cancel(attemptID string) error {
	m.mu.Lock()
	a := m.current
	m.mu.Unlock()
	if a == nil || a.id != attemptID {
		return errors.New("Unknown login")
	}
	a.cancel()
	return nil
}

func (m *manager) dismiss(attemptID string) error {
	m.mu.Lock()
	a := m.current
	if a == nil || a.id != attemptID {
		m.mu.Unlock()
		return errors.New("Unknown login")
	}
	if a.status == "working" {
		m.mu.Unlock()
		return errors.New("The login is still in progress")
	}
	m.current = nil
	m.mu.Unlock()
	m.changed()
	return nil
}

// providersOf is the state's `providers`: every provider with its login methods and how its stored credential signs in.
func providersOf(resources modelscontract.Resources) delta.JsonValue {
	snapshot := resources.Snapshot()
	signedIn := map[string]string{}
	for _, credential := range snapshot.Credentials {
		signedIn[credential.ProviderID] = credential.Type
	}
	out := make([]any, 0, len(snapshot.Providers))
	for _, provider := range snapshot.Providers {
		if len(provider.LoginMethods) == 0 && signedIn[provider.ID] == "" {
			continue
		}
		methods := make([]any, len(provider.LoginMethods))
		for i, method := range provider.LoginMethods {
			methods[i] = delta.JsonObjectOf("type", method.Type, "label", method.Label)
		}
		var signed any
		if kind, ok := signedIn[provider.ID]; ok {
			signed = kind
		}
		out = append(out, delta.JsonObjectOf("id", provider.ID, "name", provider.Name, "loginMethods", methods, "signedIn", signed))
	}
	return out
}

// Server is the login plugin's server facet.
var Server = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	m := &manager{}
	return facet.Serve(contract.ProviderLogin, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		if err != nil || !p2runtime.IsOwner(who) {
			return contracts.Implementation{}, p2runtime.Forbidden("signing in to providers is owner-only")
		}
		self.Get(modelscontract.ModelConfig, delta.NewJsonObject(0))
		native := p2runtime.NativeOf[modelscontract.ModelResourcesValue](self.Registry(modelscontract.ModelResources), "The model configuration")
		resources := func() (modelscontract.Resources, error) {
			value, err := native()
			if err != nil {
				return nil, err
			}
			return value.Resources(), nil
		}
		login := p2runtime.FollowNative(self, m.subscribe, func() (delta.JsonValue, error) { return m.login(), nil })
		providers := p2runtime.FollowNative(self,
			func(changed func()) func() {
				stop := m.subscribe(changed)
				if r, err := resources(); err == nil {
					unsubscribe := r.Subscribe(changed)
					return func() { stop(); unsubscribe() }
				}
				return stop
			},
			func() (delta.JsonValue, error) {
				r, err := resources()
				if err != nil {
					return nil, err
				}
				return providersOf(r), nil
			},
		)
		text := func(args delta.JsonValue, key string) string {
			object, _ := args.(*delta.JsonObject)
			s, _ := object.Value(key).(string)
			return s
		}
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"providers": providers, "login": login},
			Methods: map[string]contracts.MethodFunction{
				"start": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					r, err := resources()
					if err != nil {
						return nil, err
					}
					id, err := m.start(r, text(args, "providerId"), ai.AuthType(text(args, "authType")))
					if err != nil {
						return nil, err
					}
					return delta.JsonObjectOf("attemptId", id), nil
				},
				"answer": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, m.answer(text(args, "attemptId"), text(args, "promptId"), text(args, "value"))
				},
				"cancel": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, m.cancel(text(args, "attemptId"))
				},
				"dismiss": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, m.dismiss(text(args, "attemptId"))
				},
				"logout": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					r, err := resources()
					if err != nil {
						return nil, err
					}
					if err := r.Models().Logout(ctx, text(args, "providerId")); err != nil {
						return nil, err
					}
					r.Publish()
					return nil, nil
				},
				"refresh": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					r, err := resources()
					if err != nil {
						return nil, err
					}
					id := text(args, "providerId")
					if err := r.RefreshModels(ctx, id); err != nil {
						return nil, err
					}
					count := 0
					for _, model := range r.Snapshot().Models {
						if model.Provider == id {
							count++
						}
					}
					return delta.JsonObjectOf("models", float64(count)), nil
				},
			},
		}}, nil
	}), host.ServeOptions{})
})
