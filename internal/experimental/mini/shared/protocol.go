// Package shared implements mini's local service protocol and transports.
package shared

import (
	"context"
	"encoding/json"
)

// CommandResult reports command failure as data rather than an RPC error.
type CommandResult struct {
	OK    bool    `json:"ok"`
	Error *string `json:"error,omitempty"`
}

type ModelRef struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

type ModelSummary struct {
	ModelRef
	Name string `json:"name"`
}

type ProviderAccount struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	AuthType    string  `json:"authType"`
	Configured  bool    `json:"configured"`
	Source      *string `json:"source,omitempty"`
	Interactive bool    `json:"interactive"`
	MethodName  *string `json:"methodName,omitempty"`
}

type ModelsState struct {
	Models     []ModelSummary    `json:"models"`
	Accounts   []ProviderAccount `json:"accounts"`
	Refreshing bool              `json:"refreshing"`
}

// AuthPromptRequest carries the prompt without its process-local cancellation signal.
type AuthPromptRequest struct {
	Type        string              `json:"type"`
	Message     string              `json:"message"`
	Placeholder *string             `json:"placeholder,omitempty"`
	Options     *[]AuthPromptOption `json:"options,omitempty"`
}

type AuthPromptOption struct {
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	Description *string `json:"description,omitempty"`
}

// Token is the name-bearing half of a service token used by the RPC router.
type Token interface{ ServiceName() string }

// ServiceToken attaches a service's call and event types to its globally unique name.
type ServiceToken[API, Event any] struct{ Name string }

func (t ServiceToken[API, Event]) ServiceName() string { return t.Name }

func DefineService[API, Event any](name string) ServiceToken[API, Event] {
	return ServiceToken[API, Event]{Name: name}
}

type SessionSummary struct {
	ID        string  `json:"id"`
	Path      string  `json:"path"`
	Cwd       string  `json:"cwd"`
	CreatedAt float64 `json:"createdAt"`
}

type SessionSnapshot struct {
	SessionID   string `json:"sessionId"`
	Cwd         string `json:"cwd"`
	SessionPath string `json:"sessionPath"`
	// Lane is the harness-owned snapshot, forwarded without decoding or losing fields.
	Lane   json.RawMessage `json:"lane"`
	Models ModelsState     `json:"models"`
}

// ModelsEvent is state, prompt, or notice; only the corresponding payload is present.
type ModelsEvent struct {
	Type      string             `json:"type"`
	State     *ModelsState       `json:"state,omitempty"`
	RequestID *string            `json:"requestId,omitempty"`
	Request   *AuthPromptRequest `json:"request,omitempty"`
	Notice    json.RawMessage    `json:"notice,omitempty"`
}

// AuthEventPayload is the prompt/notice half of the models event channel.
type AuthEventPayload struct {
	Type      string             `json:"type"`
	RequestID *string            `json:"requestId,omitempty"`
	Request   *AuthPromptRequest `json:"request,omitempty"`
	Notice    json.RawMessage    `json:"notice,omitempty"`
}

type LaneSubscription struct {
	SubscriptionID string          `json:"subscriptionId"`
	Snapshot       SessionSnapshot `json:"snapshot"`
}

type LaneEvent struct {
	SubscriptionID string `json:"subscriptionId"`
	// Event is the harness-owned event, forwarded without decoding or losing fields.
	Event json.RawMessage `json:"event"`
}

// Service APIs use function fields so the same shape can describe local implementations and remote calls. Context is process-local and never enters the wire args.
type LaneServiceApi struct {
	Watch    func(context.Context, string) (LaneSubscription, error)
	Start    func(context.Context, string) error
	Unwatch  func(context.Context, string) error
	Prompt   func(context.Context, string) (CommandResult, error)
	Steer    func(context.Context, string) (CommandResult, error)
	FollowUp func(context.Context, string) (CommandResult, error)
	Compact  func(context.Context) (CommandResult, error)
	Abort    func(context.Context) (CommandResult, error)
	SetModel func(context.Context, ModelRef) (CommandResult, error)
}

type ModelsServiceApi struct {
	Refresh   func(context.Context) (CommandResult, error)
	Login     func(context.Context, string, string) (CommandResult, error)
	AuthReply func(context.Context, string, *string) error
}

type WorkerDescription struct {
	SessionID string `json:"sessionId"`
}

type WorkerServiceApi struct {
	Describe func(context.Context) (WorkerDescription, error)
}

type SessionsServiceApi struct {
	List   func(context.Context) ([]SessionSummary, error)
	Attach func(context.Context, *string, string, string) (string, error)
}

var (
	Lane     = DefineService[LaneServiceApi, LaneEvent]("lane")
	Models   = DefineService[ModelsServiceApi, ModelsEvent]("models")
	Worker   = DefineService[WorkerServiceApi, struct{}]("worker")
	Sessions = DefineService[SessionsServiceApi, struct{}]("sessions")
)
