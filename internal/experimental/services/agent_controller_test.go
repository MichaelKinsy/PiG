package services

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// The exact upstream agent-controller.ts declaration is the independent contract.
func TestAgentControllerContract(t *testing.T) {
	if AgentControllerID != "pi.agent-controller" {
		t.Fatalf("service id = %q", AgentControllerID)
	}
	contract := reflect.TypeFor[AgentController]()
	methods := map[string]any{
		"Prompt":       func(context.Context, AgentPromptRequest) (AgentOperationResponse, error) { panic("signature only") },
		"RequestAbort": func(context.Context, string) error { panic("signature only") },
		"Steer":        func(context.Context, AgentPromptRequest) (AgentQueueResponse, error) { panic("signature only") },
		"FollowUp":     func(context.Context, AgentPromptRequest) (AgentQueueResponse, error) { panic("signature only") },
		"NextRun":      func(context.Context, AgentPromptRequest) (AgentQueueResponse, error) { panic("signature only") },
		"CancelQueued": func(context.Context, string) (AgentCancelQueuedResponse, error) { panic("signature only") },
		"Resume":       func(context.Context) (AgentOperationResponse, error) { panic("signature only") },
		"Compact":      func(context.Context, AgentCompactionRequest) (AgentOperationResponse, error) { panic("signature only") },
		"Navigate":     func(context.Context, AgentNavigationRequest) (AgentOperationResponse, error) { panic("signature only") },
	}
	if contract.NumMethod() != len(methods) {
		t.Fatalf("controller methods = %d; want %d", contract.NumMethod(), len(methods))
	}
	for name, signature := range methods {
		method, ok := contract.MethodByName(name)
		if !ok || method.Type != reflect.TypeOf(signature) {
			t.Errorf("%s signature = %v; want %v", name, method.Type, reflect.TypeOf(signature))
		}
	}
}

func TestAgentControllerJSON(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"prompt null images", AgentPromptRequest{Message: "hello"}, `{"message":"hello","images":null}`},
		{"prompt empty images", AgentPromptRequest{Images: []AgentPromptImage{}}, `{"message":"","images":[]}`},
		{"image", AgentPromptImage{Type: "image", Data: "AA==", MimeType: "image/png"}, `{"type":"image","data":"AA==","mimeType":"image/png"}`},
		{"accepted", AgentOperationResponse{Accepted: true, OperationID: new("op")}, `{"accepted":true,"operationId":"op","error":null}`},
		{"accepted failure", AgentOperationResponse{Accepted: true, OperationID: new("op"), Error: &AgentOperationError{Code: "provider", Message: "failed"}}, `{"accepted":true,"operationId":"op","error":{"code":"provider","message":"failed"}}`},
		{"rejected", AgentOperationResponse{Error: &AgentOperationError{Code: "closed", Message: "closed"}}, `{"accepted":false,"operationId":null,"error":{"code":"closed","message":"closed"}}`},
		{"queue accepted", AgentQueueResponse{Accepted: true, EntryID: new("entry")}, `{"accepted":true,"entryId":"entry","error":null}`},
		{"queue rejected", AgentQueueResponse{Error: &AgentOperationError{Code: "closed", Message: "closed"}}, `{"accepted":false,"entryId":null,"error":{"code":"closed","message":"closed"}}`},
		{"compact", AgentCompactionRequest{}, `{"customInstructions":null}`},
		{"navigate", AgentNavigationRequest{}, `{"targetId":null,"summarize":false,"label":null,"customInstructions":null}`},
		{"navigate empty strings", AgentNavigationRequest{TargetID: new(""), Summarize: true, Label: new(""), CustomInstructions: new("")}, `{"targetId":"","summarize":true,"label":"","customInstructions":""}`},
		{"cancelled", AgentCancelQueuedResponse{Outcome: "cancelled"}, `{"outcome":"cancelled"}`},
		{"consumed", AgentCancelQueuedResponse{Outcome: "already_consumed"}, `{"outcome":"already_consumed"}`},
		{"missing", AgentCancelQueuedResponse{Outcome: "not_found"}, `{"outcome":"not_found"}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.value)
			if err != nil || string(got) != tt.want {
				t.Fatalf("marshal = %s, %v; want %s", got, err, tt.want)
			}
			decoded := reflect.New(reflect.TypeOf(tt.value))
			if err := json.Unmarshal(got, decoded.Interface()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.Elem().Interface(), tt.value) {
				t.Fatalf("round trip = %#v; want %#v", decoded.Elem().Interface(), tt.value)
			}
		})
	}
}
