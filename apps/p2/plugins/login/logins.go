package login

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/login/logins.ts. Provider logins (provider-owned api-key and OAuth flows over the shared
// credentials), one at a time, each belonging to the owner (principal key) that started it. The flow runs here, outside
// Chord; its state changes synchronously with each event and listeners hear of every change (the server's `LoginService`
// follows it).

// LoginDeadline ends a login nobody finished.
// LoginDeadline ends a login nobody finished (Pi: ten minutes; a variable for tests).
var LoginDeadline = 600 * time.Second

var deviceCodeOption = regexp.MustCompile(`^device[-_]code$`)

type pendingPrompt struct {
	kind    string
	options []string
	resolve func(string)
}

type attempt struct {
	id, owner, providerID string
	remote                bool
	cancel                context.CancelFunc
	prompts               map[string]*pendingPrompt
	// committed is set once the provider's login returned a credential; finished once the state shows the end (guarded by Logins.mu).
	committed, finished bool
	done                chan struct{}
}

// loginState is `LoginState` of the contract.
type loginState struct {
	attemptID, providerID string
	authType              ai.AuthType
	status                string // working, success, cancelled, error
	prompt                *delta.JsonObject
	notices               []any
	message               string
}

func (s *loginState) json() delta.JsonValue {
	if s == nil {
		return nil
	}
	out := delta.JsonObjectOf("attemptId", s.attemptID, "providerId", s.providerID, "authType", string(s.authType), "status", s.status)
	if s.prompt != nil {
		out.Set("prompt", s.prompt)
	} else {
		out.Set("prompt", nil)
	}
	out.Set("notices", append([]any{}, s.notices...))
	if s.message != "" {
		out.Set("message", s.message)
	}
	return out
}

// loginsState is the current login (one at a time) and the principal key of the owner that started it.
type loginsState struct {
	login *loginState
	owner string
}

// Logins is the provider logins of one server.
type Logins struct {
	directory string

	lifetime context.Context
	stop     context.CancelFunc

	mu        sync.Mutex
	listeners map[int]func()
	nextID    int
	// publish serializes the publications of the state: a listener reads the state and gives it to the host (which takes the host's
	// loop), and two of them in flight could apply in the other order, the older read last. It is held across the read and the
	// apply, so every caller of changed must be off the host's loop (the methods give it up first: chordctx.Suspend), or it
	// would wait for the loop while a publication in flight holds this.
	publish sync.Mutex
	active  *attempt
	current loginsState
	device  sync.Mutex
}

// NewLogins is the logins keeping their device identity in directory. The resources a method works on come from its caller,
// read on the host's loop: the registry they live in is not safe to read from the logins' own goroutines.
func NewLogins(directory string) *Logins {
	lifetime, stop := context.WithCancel(context.Background())
	return &Logins{directory: directory, lifetime: lifetime, stop: stop, listeners: map[int]func(){}}
}

// State is the current login and its owner.
func (l *Logins) State() (login delta.JsonValue, owner delta.JsonValue) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current.login == nil {
		return nil, nil
	}
	return l.current.login.json(), l.current.owner
}

// Subscribe calls listener after every change of the state; the result stops it.
func (l *Logins) Subscribe(listener func()) func() {
	l.mu.Lock()
	l.nextID++
	id := l.nextID
	l.listeners[id] = listener
	l.mu.Unlock()
	return func() { l.mu.Lock(); delete(l.listeners, id); l.mu.Unlock() }
}

func (l *Logins) changed() {
	l.publish.Lock()
	defer l.publish.Unlock()
	l.mu.Lock()
	listeners := make([]func(), 0, len(l.listeners))
	for _, listener := range l.listeners {
		listeners = append(listeners, listener)
	}
	l.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Start begins owner's login of providerID with authType and returns its attempt id.
func (l *Logins) Start(resources modelscontract.Resources, owner, providerID string, authType ai.AuthType, remote bool) (string, error) {
	if l.lifetime.Err() != nil {
		return "", errors.New("Server is shutting down")
	}
	l.mu.Lock()
	if running := l.active; running != nil {
		l.mu.Unlock()
		if running.owner == owner {
			return "", errors.New("A provider login is already in progress; cancel it first")
		}
		return "", errors.New("Another owner device is signing in to a provider; try again when it has finished")
	}
	provider := resources.Models().GetProvider(providerID)
	supported := false
	if provider != nil {
		if authType == ai.CredentialOAuth {
			supported = provider.Auth.OAuth != nil && provider.Auth.OAuth.Login != nil
		} else {
			supported = provider.Auth.APIKey != nil && provider.Auth.APIKey.Login != nil
		}
	}
	if !supported {
		l.mu.Unlock()
		return "", errors.New("Provider does not support this login method")
	}
	ctx, cancel := context.WithCancel(l.lifetime)
	a := &attempt{id: newID(), owner: owner, providerID: providerID, remote: remote, cancel: cancel, prompts: map[string]*pendingPrompt{}, done: make(chan struct{})}
	l.active = a
	l.current = loginsState{owner: owner, login: &loginState{attemptID: a.id, providerID: providerID, authType: authType, status: "working", notices: []any{}}}
	l.mu.Unlock()
	l.changed()
	go l.run(ctx, a, resources, authType)
	return a.id, nil
}

// jsonOf is a value as a chord JSON object, through its JSON text.
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

// update applies mutate to the current login if it is a's, then tells the listeners.
func (l *Logins) update(a *attempt, mutate func(*loginState)) {
	l.mu.Lock()
	if l.current.login != nil && l.current.login.attemptID == a.id {
		mutate(l.current.login)
	}
	l.mu.Unlock()
	l.changed()
}

func (l *Logins) notice(a *attempt, event ai.AuthEvent) {
	object, err := jsonOf(event)
	if err != nil {
		return
	}
	switch typed := event.(type) {
	case ai.AuthDeviceCodeEvent:
		p2runtime.ProcessLogs.Redactor.AddSecret(typed.UserCode)
	case ai.AuthURLEvent:
		p2runtime.ProcessLogs.Redactor.AddSecret(typed.URL)
	}
	l.update(a, func(s *loginState) {
		s.notices = append(s.notices, delta.JsonObjectOf("id", float64(len(s.notices)), "event", object))
	})
}

func (l *Logins) prompt(ctx context.Context, a *attempt, input ai.AuthPrompt) (string, error) {
	// pi-ai's method selectors use device_code (Codex) or device-code (Radius). Other selectors (accounts, organizations)
	// still require the owner's answer.
	if selected, ok := input.(ai.AuthSelectPrompt); ok && a.remote {
		for _, option := range selected.Options {
			if deviceCodeOption.MatchString(option.ID) {
				if ctx.Err() != nil {
					return "", context.Cause(ctx)
				}
				l.notice(a, ai.AuthInfoEvent{Message: "Using device-code login on this remote connection."})
				return option.ID, nil
			}
		}
	}
	object, err := jsonOf(input)
	if err != nil {
		return "", err
	}
	id := newID()
	object.Set("id", id)
	answers := make(chan string, 1)
	// close withdraws the question: at once when answered (the answer follows its state: the answered prompt is closed when
	// the call resolves), or when the flow is aborted.
	closePrompt := func() {
		l.mu.Lock()
		_, open := a.prompts[id]
		delete(a.prompts, id)
		l.mu.Unlock()
		if open {
			l.update(a, func(s *loginState) {
				if s.prompt != nil && s.prompt.Value("id") == id {
					s.prompt = nil
				}
			})
		}
	}
	pending := &pendingPrompt{kind: input.Type(), resolve: func(value string) {
		closePrompt()
		select {
		case answers <- value:
		default:
		}
	}}
	if selected, ok := input.(ai.AuthSelectPrompt); ok {
		for _, option := range selected.Options {
			pending.options = append(pending.options, option.ID)
		}
	}
	l.mu.Lock()
	a.prompts[id] = pending
	l.mu.Unlock()
	l.update(a, func(s *loginState) { s.prompt = object })
	select {
	case value := <-answers:
		return value, nil
	case <-ctx.Done():
		closePrompt()
		return "", context.Cause(ctx)
	}
}

// deviceID is this installation's stable id (device.json), made on first use.
func (l *Logins) deviceID() (string, error) {
	l.device.Lock()
	defer l.device.Unlock()
	path := filepath.Join(l.directory, "device.json")
	var data struct {
		ID string `json:"id"`
	}
	content, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(content, &data); err != nil {
			return "", errors.New("Invalid device identity file")
		}
		if data.ID != "" {
			return data.ID, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	data.ID = newID()
	out, _ := json.Marshal(data)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return "", err
	}
	return data.ID, nil
}

// finish shows the end of a's login unless it is shown already; it reports whether it did.
func (l *Logins) finish(a *attempt, status, message string) bool {
	l.mu.Lock()
	if a.finished {
		l.mu.Unlock()
		return false
	}
	a.finished = true
	if l.current.login != nil && l.current.login.attemptID == a.id {
		l.current.login.status, l.current.login.prompt = status, nil
		if message != "" {
			l.current.login.message = message
		}
	}
	l.mu.Unlock()
	l.changed()
	return true
}

func (l *Logins) run(parent context.Context, a *attempt, resources modelscontract.Resources, authType ai.AuthType) {
	ctx, release := context.WithTimeout(parent, LoginDeadline)
	defer release()
	defer func() {
		a.cancel() // Withdraw leftover prompts after callback success.
		l.mu.Lock()
		if l.active == a {
			l.active = nil
		}
		l.mu.Unlock()
		close(a.done)
	}()
	deviceID, err := l.deviceID()
	if err == nil {
		interaction := ai.AuthInteraction{
			Prompt: func(promptCtx context.Context, prompt ai.AuthPrompt) (string, error) {
				merged, stop := context.WithCancelCause(ctx)
				defer stop(nil)
				go func() {
					select {
					case <-promptCtx.Done():
						stop(context.Cause(promptCtx))
					case <-merged.Done():
					}
				}()
				return l.prompt(merged, a, prompt)
			},
			Notify: func(event ai.AuthEvent) {
				if ctx.Err() == nil {
					l.notice(a, event)
				}
			},
		}
		// Provider-owned login/refresh only. Never return the credential to a client.
		_, err = resources.Models().Login(ctx, a.providerID, authType, interaction, ai.LoginOptions{GetDeviceID: func() string { return deviceID }})
		if err == nil {
			l.mu.Lock()
			a.committed = true
			l.mu.Unlock()
			resources.Publish()
			message := ""
			if refreshErr := resources.RefreshModels(ctx, a.providerID); refreshErr != nil {
				message = "Signed in. Model-list refresh did not complete; use Refresh models to retry."
			}
			l.finish(a, "success", message)
			return
		}
	}
	l.mu.Lock()
	committed := a.committed
	l.mu.Unlock()
	switch {
	case committed:
		l.finish(a, "success", "Signed in; resource refresh did not complete.")
	case ctx.Err() != nil:
		l.finish(a, "cancelled", "Login cancelled.")
	default:
		l.finish(a, "error", "Provider login failed. Check the provider configuration and try again.")
	}
}

func (l *Logins) owned(owner, id string) (*attempt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active == nil || l.active.id != id || l.active.owner != owner {
		return nil, errors.New("Login attempt is no longer active for this connection")
	}
	return l.active, nil
}

// Answer answers the prompt (closed at once); secrets and pasted codes are redacted from the logs first.
func (l *Logins) Answer(owner, attemptID, promptID, value string) error {
	a, err := l.owned(owner, attemptID)
	if err != nil {
		return err
	}
	l.mu.Lock()
	pending := a.prompts[promptID]
	l.mu.Unlock()
	if pending == nil {
		return errors.New("Login prompt is no longer active")
	}
	if pending.kind == "select" {
		listed := false
		for _, option := range pending.options {
			listed = listed || option == value
		}
		if !listed {
			return errors.New("Select one of the listed options")
		}
	}
	if pending.kind == "secret" || pending.kind == "manual_code" {
		p2runtime.ProcessLogs.Redactor.AddSecret(value)
	}
	pending.resolve(value)
	return nil
}

// Cancel cancels the login; its state shows "cancelled" when the call returns (or what it reached first). A call of the
// host runs on its loop, which the login's goroutine needs to publish its end, so Cancel shows the end itself and never
// waits for the goroutine.
func (l *Logins) Cancel(owner, attemptID string) error {
	a, err := l.owned(owner, attemptID)
	if err != nil {
		return err
	}
	a.cancel()
	l.mu.Lock()
	committed := a.committed
	l.mu.Unlock()
	if !committed {
		l.finish(a, "cancelled", "Login cancelled.")
	}
	return nil
}

// Dismiss clears owner's finished login attemptID from the state.
func (l *Logins) Dismiss(owner, attemptID string) {
	l.mu.Lock()
	login := l.current.login
	if l.current.owner != owner || login == nil || login.attemptID != attemptID || login.status == "working" {
		l.mu.Unlock()
		return
	}
	l.current.login = nil
	l.mu.Unlock()
	l.changed()
}

// Disconnect ends owner's unfinished login, nobody else's: its owner left (closed the sheet everywhere, or disconnected).
func (l *Logins) Disconnect(owner string) {
	l.mu.Lock()
	a := l.active
	l.mu.Unlock()
	if a != nil && a.owner == owner {
		a.cancel()
	}
}

// Logout removes providerID's credentials and stored models.
func (l *Logins) Logout(ctx context.Context, resources modelscontract.Resources, providerID string) error {
	l.mu.Lock()
	busy := l.active != nil
	l.mu.Unlock()
	if busy {
		return errors.New("Cancel the active login before removing credentials")
	}
	if err := resources.Models().Logout(ctx, providerID); err != nil {
		return err
	}
	if stored, ok := resources.(interface {
		DeleteStoredModels(context.Context, string) error
	}); ok {
		if err := stored.DeleteStoredModels(ctx, providerID); err != nil {
			return err
		}
	}
	if err := resources.Reload(ctx); err != nil {
		return err
	}
	resources.Publish()
	return nil
}

// Refresh fetches providerID's current model list and returns how many models it has.
func (l *Logins) Refresh(ctx context.Context, resources modelscontract.Resources, providerID string) (int, error) {
	if err := resources.RefreshModels(ctx, providerID); err != nil {
		return 0, err
	}
	count := 0
	for _, model := range resources.Snapshot().Models {
		if model.Provider == providerID {
			count++
		}
	}
	return count, nil
}

// Close ends the logins: the one in progress is cancelled (its goroutine ends on its own; the host's loop may be what it waits for).
func (l *Logins) Close() {
	l.stop()
	l.mu.Lock()
	a := l.active
	l.listeners = map[int]func(){}
	l.mu.Unlock()
	if a != nil {
		a.cancel()
	}
}
