package experimental

// Ports packages/coding-agent/src/experimental/session-worker.ts

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

type WorkerOperationRequest struct {
	Type      string               `json:"type"`
	RequestID string               `json:"requestId"`
	Scope     WorkerOperationScope `json:"scope"`
	Call      chord.ServiceCall    `json:"call"`
}

type workerActiveRequest struct {
	scope  WorkerOperationScope
	cancel context.CancelCauseFunc
}
type workerActiveRequests struct {
	mu     sync.Mutex
	values map[string]*workerActiveRequest
	work   sync.WaitGroup
}

func (r *workerActiveRequests) cancelWhere(matches func(WorkerOperationScope) bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, request := range r.values {
		if matches(request.scope) {
			request.cancel(err)
		}
	}
}
func (r *workerActiveRequests) close() {
	r.cancelWhere(func(WorkerOperationScope) bool { return true }, errors.New("Session worker is closing"))
	r.work.Wait()
}

func validWorkerServiceCall(raw json.RawMessage, call chord.ServiceCall) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || call.ServiceId == "" || call.Member == "" || call.Args == nil {
		return false
	}
	for key := range fields {
		switch key {
		case "serviceId", "member", "args", "instance":
		default:
			return false
		}
	}
	if instance, present := fields["instance"]; present {
		var keys map[string]json.RawMessage
		if json.Unmarshal(instance, &keys) != nil || len(keys) != 2 || call.Instance == nil || call.Instance.Key == "" || call.Instance.Generation < 1 {
			return false
		}
	}
	return true
}
func handleWorkerCommand(raw json.RawMessage, control *workerControlConnection, lifecycle *WorkerLifecycle, requests *workerActiveRequests, workerServices *services.SessionWorkerServices, token, sessionKey string, announce func() error, retire func()) error {
	var envelope map[string]json.RawMessage
	message, err := decodeControlMessage(raw)
	if err != nil || json.Unmarshal(raw, &envelope) != nil {
		return errors.New("Coordinator sent an invalid worker message")
	}
	switch message.Type {
	case "server_connected", "server_disconnected":
		if !stringMember(envelope, "serverConnectionId") {
			return errors.New("Coordinator sent an invalid worker message")
		}
		if message.Type == "server_connected" {
			lifecycle.ServerConnected(message.ServerConnectionID)
		} else {
			workerServices.RemoveSubscriptions(func(scope services.WorkerServiceScope) bool {
				return scope.ServerConnectionId == message.ServerConnectionID
			})
			requests.cancelWhere(func(scope WorkerOperationScope) bool { return scope.ServerConnectionID == message.ServerConnectionID }, errors.New("Server disconnected"))
			lifecycle.ServerDisconnected(message.ServerConnectionID)
		}
		return nil
	case "peer_registered":
		if !stringMember(envelope, "peerId") {
			return errors.New("Coordinator sent an invalid worker message")
		}
		return nil
	case "message":
		if message.From != "server" || len(message.Payload) == 0 {
			return errors.New("Coordinator sent an invalid worker message")
		}
	default:
		return errors.New("Coordinator sent an invalid worker message")
	}
	var fields map[string]json.RawMessage
	var command struct {
		Type               string               `json:"type"`
		ServerConnectionID string               `json:"serverConnectionId"`
		RequestID          string               `json:"requestId"`
		AttachmentID       string               `json:"attachmentId"`
		Attached           *bool                `json:"attached"`
		Scope              WorkerOperationScope `json:"scope"`
		Call               chord.ServiceCall    `json:"call"`
	}
	if json.Unmarshal(message.Payload, &fields) != nil || json.Unmarshal(message.Payload, &command) != nil {
		return nil
	}
	switch command.Type {
	case "shutdown":
		retire()
	case "discover_workers":
		if err := announce(); err != nil {
			retire()
		}
	case "session_demand":
		if !stringMember(fields, "serverConnectionId") || !stringMember(fields, "requestId") || !stringMember(fields, "attachmentId") || command.Attached == nil {
			return nil
		}
		release := lifecycle.HoldRetirement()
		defer release()
		if !*command.Attached {
			workerServices.RemoveSubscriptions(func(scope services.WorkerServiceScope) bool {
				return scope.ServerConnectionId == command.ServerConnectionID && scope.AttachmentId == command.AttachmentID
			})
		}
		if err := lifecycle.SetDemand(command.ServerConnectionID, command.AttachmentID, *command.Attached); err != nil {
			return control.send(map[string]any{"type": "demand_rejected", "token": token, "sessionKey": sessionKey, "requestId": command.RequestID, "message": err.Error()})
		}
		return control.send(map[string]any{"type": "demand_applied", "token": token, "sessionKey": sessionKey, "requestId": command.RequestID, "attachmentId": command.AttachmentID, "attached": *command.Attached})
	case "operation_cancel":
		if len(fields) != 3 || command.RequestID == "" || !scopeValid(fields["scope"]) {
			return nil
		}
		requests.mu.Lock()
		if request := requests.values[command.RequestID]; request != nil && request.scope == command.Scope {
			request.cancel(errors.New("Service operation cancelled"))
		}
		requests.mu.Unlock()
	case "operation":
		if len(fields) != 4 || command.RequestID == "" || !scopeValid(fields["scope"]) || !validWorkerServiceCall(fields["call"], command.Call) {
			return nil
		}
		request := WorkerOperationRequest{Type: command.Type, RequestID: command.RequestID, Scope: command.Scope, Call: command.Call}
		// Request admission and lifetime holds precede asynchronous service work, as in the synchronous prefix of handleOperation.
		release, admissionError := lifecycle.BeginRequest(request.Scope.ServerConnectionID, request.Scope.AttachmentID)
		ctx, cancel := context.WithCancelCause(context.Background())
		active := &workerActiveRequest{scope: request.Scope, cancel: cancel}
		var invocation *chord.ServiceInvocation
		if admissionError == nil {
			requests.mu.Lock()
			requests.values[request.RequestID] = active
			requests.mu.Unlock()
			// Begin the call here, in message order: upstream's handleOperation reaches the endpoint synchronously.
			invocation, admissionError = workerServices.BeginInvoke(ctx, request.Call, services.WorkerServiceScope{ServerConnectionId: request.Scope.ServerConnectionID, AttachmentId: request.Scope.AttachmentID})
		}
		requests.work.Go(func() {
			defer cancel(nil)
			defer func() {
				requests.mu.Lock()
				if requests.values[request.RequestID] == active {
					delete(requests.values, request.RequestID)
				}
				requests.mu.Unlock()
				if release != nil {
					release()
				}
			}()
			result, err := json.RawMessage(nil), admissionError
			if err == nil {
				result, err = invocation.Wait(context.Background())
			}
			response := map[string]any{"requestId": request.RequestID, "scope": request.Scope}
			if err == nil && len(result) != 0 && !json.Valid(result) {
				err = errors.New("Service produced a non-JSON result")
			}
			if err == nil {
				response["type"] = "operation_result"
				if result != nil {
					response["result"] = result
				}
			} else {
				response["type"] = "operation_error"
				response["message"] = err.Error()
				if serviceError, ok := errors.AsType[*chord.RemoteServiceError](err); ok {
					response["code"] = serviceError.Code
				}
			}
			if err := control.send(map[string]any{"type": "operation_response", "token": token, "sessionKey": sessionKey, "response": response}); err != nil {
				retire()
			}
		})
	}
	return nil
}
