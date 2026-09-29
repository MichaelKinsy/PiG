package ai

// Ports packages/ai/src/auth/types.ts (AuthInteraction, AuthPrompt and AuthEvent).

import (
	"context"
	"encoding/json"
)

type AuthType = CredentialType

// AuthPrompt is a closed login-prompt union. The callback context carries the per-prompt signal.
type AuthPrompt interface {
	authPrompt()
	Type() string
}
type AuthTextPrompt struct {
	Message     string `json:"message"`
	Placeholder string `json:"placeholder,omitempty"`
}
type AuthSecretPrompt struct {
	Message     string `json:"message"`
	Placeholder string `json:"placeholder,omitempty"`
}
type AuthManualCodePrompt struct {
	Message     string `json:"message"`
	Placeholder string `json:"placeholder,omitempty"`
}
type AuthSelectOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}
type AuthSelectPrompt struct {
	Message string             `json:"message"`
	Options []AuthSelectOption `json:"options"`
}

func (AuthTextPrompt) authPrompt()        {}
func (AuthSecretPrompt) authPrompt()      {}
func (AuthManualCodePrompt) authPrompt()  {}
func (AuthSelectPrompt) authPrompt()      {}
func (AuthTextPrompt) Type() string       { return "text" }
func (AuthSecretPrompt) Type() string     { return "secret" }
func (AuthManualCodePrompt) Type() string { return "manual_code" }
func (AuthSelectPrompt) Type() string     { return "select" }

type AuthInfoLink struct {
	URL   string `json:"url"`
	Label string `json:"label,omitempty"`
}
type AuthEvent interface {
	authEvent()
	Type() string
}
type AuthInfoEvent struct {
	Message string         `json:"message"`
	Links   []AuthInfoLink `json:"links,omitempty"`
}
type AuthURLEvent struct {
	URL          string `json:"url"`
	Instructions string `json:"instructions,omitempty"`
}
type AuthDeviceCodeEvent struct {
	UserCode         string   `json:"userCode"`
	VerificationURI  string   `json:"verificationUri"`
	IntervalSeconds  *float64 `json:"intervalSeconds,omitempty"`
	ExpiresInSeconds *float64 `json:"expiresInSeconds,omitempty"`
}
type AuthProgressEvent struct {
	Message string `json:"message"`
}

func (AuthInfoEvent) authEvent()         {}
func (AuthURLEvent) authEvent()          {}
func (AuthDeviceCodeEvent) authEvent()   {}
func (AuthProgressEvent) authEvent()     {}
func (AuthInfoEvent) Type() string       { return "info" }
func (AuthURLEvent) Type() string        { return "auth_url" }
func (AuthDeviceCodeEvent) Type() string { return "device_code" }
func (AuthProgressEvent) Type() string   { return "progress" }

// AuthInteraction supplies awaited prompts and synchronous notifications. Login receives its owning context separately.
type AuthInteraction struct {
	Prompt func(context.Context, AuthPrompt) (string, error)
	Notify func(AuthEvent)
}

func marshalAuthVariant(kind string, value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	tag, err := json.Marshal(kind)
	if err != nil {
		return nil, err
	}
	out := append([]byte(`{"type":`), tag...)
	if len(body) > 2 {
		out = append(out, ',')
		out = append(out, body[1:]...)
	} else {
		out = append(out, '}')
	}
	return out, nil
}
func (value AuthTextPrompt) MarshalJSON() ([]byte, error) {
	type fields AuthTextPrompt
	return marshalAuthVariant(value.Type(), fields(value))
}
func (value AuthSecretPrompt) MarshalJSON() ([]byte, error) {
	type fields AuthSecretPrompt
	return marshalAuthVariant(value.Type(), fields(value))
}
func (value AuthManualCodePrompt) MarshalJSON() ([]byte, error) {
	type fields AuthManualCodePrompt
	return marshalAuthVariant(value.Type(), fields(value))
}
func (value AuthSelectPrompt) MarshalJSON() ([]byte, error) {
	type fields AuthSelectPrompt
	return marshalAuthVariant(value.Type(), fields(value))
}
func (value AuthInfoEvent) MarshalJSON() ([]byte, error) {
	type fields AuthInfoEvent
	return marshalAuthVariant(value.Type(), fields(value))
}
func (value AuthURLEvent) MarshalJSON() ([]byte, error) {
	type fields AuthURLEvent
	return marshalAuthVariant(value.Type(), fields(value))
}
func (value AuthDeviceCodeEvent) MarshalJSON() ([]byte, error) {
	type fields AuthDeviceCodeEvent
	return marshalAuthVariant(value.Type(), fields(value))
}
func (value AuthProgressEvent) MarshalJSON() ([]byte, error) {
	type fields AuthProgressEvent
	return marshalAuthVariant(value.Type(), fields(value))
}
