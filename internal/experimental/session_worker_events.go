package experimental

// Ports packages/coding-agent/src/experimental/session-worker-manager.ts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

func encodeSessionKey(path string) string { return base64.RawURLEncoding.EncodeToString([]byte(path)) }

type sessionWorkerEvent struct {
	Type                string                   `json:"type"`
	Token               string                   `json:"token"`
	SessionKey          string                   `json:"sessionKey"`
	SessionID           string                   `json:"sessionId"`
	PID                 int                      `json:"-"`
	Metadata            SessionCatalogMetadata   `json:"-"`
	PluginManifestPaths []string                 `json:"pluginManifestPaths"`
	RequestID           string                   `json:"requestId"`
	AttachmentID        string                   `json:"attachmentId"`
	Attached            *bool                    `json:"attached"`
	Message             string                   `json:"message"`
	Response            *workerOperationResponse `json:"response"`
	Scope               *WorkerOperationScope    `json:"scope"`
	SubscriptionID      string                   `json:"subscriptionId"`
	Update              json.RawMessage          `json:"update"`
}
type workerOperationResponse struct {
	Type      string                       `json:"type"`
	RequestID string                       `json:"requestId"`
	Scope     *WorkerOperationScope        `json:"scope"`
	Result    json.RawMessage              `json:"result"`
	Code      chord.RemoteServiceErrorCode `json:"code"`
	Message   string                       `json:"message"`
}

func stringMember(fields map[string]json.RawMessage, key string) bool {
	raw := fields[key]
	return len(raw) > 0 && raw[0] == '"'
}
func scopeValid(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	return json.Unmarshal(raw, &fields) == nil && len(fields) == 2 && stringMember(fields, "serverConnectionId") && stringMember(fields, "attachmentId")
}

// decodeSessionWorkerMetadata validates metadata against the StrictObject SessionWorkerMetadataSchema (session-worker.ts:69-74): exactly id (a non-empty string), createdAt (any number), cwd and path (strings). Members are read by exact key, because a case-variant key such as CreatedAt is an additional property to TypeBox, while encoding/json matches struct tags case-insensitively.
func decodeSessionWorkerMetadata(raw json.RawMessage) (SessionCatalogMetadata, bool) {
	var fields map[string]json.RawMessage
	var value SessionCatalogMetadata
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 4 || !stringMember(fields, "id") || !stringMember(fields, "cwd") || !stringMember(fields, "path") {
		return value, false
	}
	var createdAt *float64
	rawCreatedAt, present := fields["createdAt"]
	if !present || json.Unmarshal(rawCreatedAt, &createdAt) != nil || createdAt == nil {
		return value, false
	}
	if json.Unmarshal(fields["id"], &value.ID) != nil || value.ID == "" || json.Unmarshal(fields["cwd"], &value.Cwd) != nil || json.Unmarshal(fields["path"], &value.Path) != nil {
		return SessionCatalogMetadata{}, false
	}
	value.CreatedAt = *createdAt
	return value, true
}

// integerMember decodes a Type.Integer({minimum}) member: Number.isInteger of the JSON.parse value, so 1e3 and 1.0 are integers.
func integerMember(fields map[string]json.RawMessage, key string, minimum int) (int, bool) {
	raw := fields[key]
	if len(raw) == 0 || raw[0] == '"' || raw[0] == 'n' {
		return 0, false
	}
	var value float64
	if json.Unmarshal(raw, &value) != nil || value != math.Trunc(value) || value < float64(minimum) || value >= float64(math.MaxInt) {
		return 0, false
	}
	return int(value), true
}

func decodeSessionWorkerEvent(raw json.RawMessage) (sessionWorkerEvent, error) {
	var event sessionWorkerEvent
	var fields map[string]json.RawMessage
	invalid := errors.New("invalid Session worker event")
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(raw, &event) != nil || !stringMember(fields, "type") || !stringMember(fields, "token") || !stringMember(fields, "sessionKey") {
		return event, invalid
	}
	switch event.Type {
	case "worker_ready":
		pid, validPID := integerMember(fields, "pid", 1)
		if !stringMember(fields, "sessionId") || !validPID || event.PluginManifestPaths == nil {
			return event, invalid
		}
		event.PID = pid
		if slices.Contains(event.PluginManifestPaths, "") {
			return event, invalid
		}
		metadata, ok := decodeSessionWorkerMetadata(fields["metadata"])
		if !ok {
			return event, invalid
		}
		event.Metadata = metadata
	case "worker_failed":
		if !stringMember(fields, "message") {
			return event, invalid
		}
	case "demand_applied":
		if !stringMember(fields, "requestId") || !stringMember(fields, "attachmentId") || event.Attached == nil {
			return event, invalid
		}
	case "demand_rejected":
		if !stringMember(fields, "requestId") || !stringMember(fields, "message") {
			return event, invalid
		}
	case "operation_response":
		var response map[string]json.RawMessage
		if json.Unmarshal(fields["response"], &response) != nil || event.Response == nil || event.Response.RequestID == "" || !scopeValid(response["scope"]) {
			return event, invalid
		}
		allowed := map[string]bool{"type": true, "requestId": true, "scope": true}
		switch event.Response.Type {
		case "operation_result":
			allowed["result"] = true
		case "operation_error":
			allowed["code"] = true
			allowed["message"] = true
			if !stringMember(response, "message") {
				return event, invalid
			}
			if _, present := response["code"]; present {
				switch event.Response.Code {
				case chord.ErrServiceNotAllowed, chord.ErrServiceNotFound, chord.ErrServiceModeMismatch, chord.ErrServiceMemberNotFound, chord.ErrServiceMemberMismatch, chord.ErrServiceInstanceNotFound, chord.ErrServiceStaleInstance, chord.ErrServiceInvalidValue:
				default:
					return event, invalid
				}
			}
		default:
			return event, invalid
		}
		for key := range response {
			if !allowed[key] {
				return event, invalid
			}
		}
	case "service_update":
		if !scopeValid(fields["scope"]) || event.SubscriptionID == "" || len(event.Update) == 0 {
			return event, invalid
		}
	default:
		return event, invalid
	}
	return event, nil
}

func (m *SessionWorkerManager) handleCoordinatorEvent(event CoordinatorConnectionEvent) {
	m.mu.Lock()
	if m.detached {
		m.mu.Unlock()
		return
	}
	if event.Type == "peer_disconnected" {
		m.markDiscovered(event.PeerID)
		if worker := m.workersByPeer[event.PeerID]; worker != nil {
			var failure error
			switch {
			case worker.expectedStop:
			case worker.exitReason != "":
				// session-worker-manager.ts:714-721: the child's exit was observed before this disconnection.
				failure = fmt.Errorf("Session worker %s exited unexpectedly (%s)", worker.metadata.ID, worker.exitReason)
			default:
				failure = fmt.Errorf("Session worker %s disconnected unexpectedly", worker.metadata.ID)
			}
			m.removeWorker(worker, failure)
		}
		if pending := m.pendingPeer(event.PeerID); pending != nil {
			m.failPending(pending.sessionKey, errors.New("Session worker disconnected during startup"))
		}
		m.mu.Unlock()
		m.notifyWorkerCountChanged()
		return
	}
	if event.Type != "message" {
		m.mu.Unlock()
		return
	}
	message, err := decodeSessionWorkerEvent(event.Payload)
	if err != nil {
		var envelope struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(event.Payload, &envelope) == nil && envelope.Type == "operation_response" {
			if worker := m.workersByPeer[event.From]; worker != nil {
				m.rejectWorkerOperations(worker, errors.New("Session worker returned an invalid operation response"))
			}
		}
		m.mu.Unlock()
		return
	}
	switch message.Type {
	case "worker_failed":
		if pending := m.pending[message.SessionKey]; pending != nil && pending.peerID == event.From && pending.token == message.Token {
			m.failPending(message.SessionKey, fmt.Errorf("Session worker failed: %s", message.Message))
		}
	case "demand_applied", "demand_rejected":
		pending := m.pendingDemand[message.RequestID]
		if pending != nil && pending.worker.peerID == event.From && pending.worker.token == message.Token && pending.worker.metadata.Path == message.SessionKey && (message.Type != "demand_applied" || (pending.attachmentID == message.AttachmentID && pending.attached == *message.Attached)) {
			var failure error
			if message.Type == "demand_rejected" {
				failure = fmt.Errorf("Session worker rejected demand: %s", message.Message)
			}
			m.rejectDemand(message.RequestID, failure)
		}
	case "operation_response":
		m.handleOperationResponse(event.From, message)
	case "service_update":
		m.handleServiceUpdate(event.From, message)
	case "worker_ready":
		if m.recordReadyWorker(event.From, message) {
			// session-worker-manager.ts:669 sends this shutdown without awaiting it, so a blocked write never holds the event handler.
			m.sendAsyncLocked(event.From, map[string]any{"type": "shutdown"}, nil)
		}
		m.mu.Unlock()
		m.notifyWorkerCountChanged()
		return
	}
	m.mu.Unlock()
	if message.Type == "worker_failed" {
		m.notifyWorkerCountChanged()
	}
}

func (m *SessionWorkerManager) recordReadyWorker(peerID string, message sessionWorkerEvent) bool {
	if message.SessionKey != message.Metadata.Path || message.SessionID != message.Metadata.ID || !filepath.IsAbs(message.Metadata.Cwd) || !filepath.IsAbs(message.Metadata.Path) {
		return false
	}
	m.markDiscovered(peerID)
	pending := m.pending[message.SessionKey]
	if pending != nil && pending.peerID == peerID && !slices.Equal(message.PluginManifestPaths, pending.pluginManifestPaths) {
		m.failPending(message.SessionKey, errors.New("Session worker started with stale plugin packages"))
		return false
	}
	existing := m.workersBySession[message.SessionKey]
	if existing != nil && existing.peerID != peerID {
		return true
	}
	if pending != nil && (pending.peerID != peerID || pending.token != message.Token || pending.child.PID() != message.PID) {
		return false
	}
	if existing != nil {
		return false
	}
	worker := &workerRecord{peerID: peerID, metadata: message.Metadata, pid: message.PID, token: message.Token, pluginManifestPaths: slices.Clone(message.PluginManifestPaths), terminated: make(chan struct{}), attachmentIDs: make(map[string]bool)}
	m.workersBySession[message.SessionKey] = worker
	m.workersByPeer[peerID] = worker
	m.workerOrder = append(m.workerOrder, worker)
	m.workerPids[worker.metadata.ID] = worker.pid
	if pending != nil {
		delete(m.pending, message.SessionKey)
		pending.worker = worker
		close(pending.done)
	}
	return false
}

func (m *SessionWorkerManager) handleOperationResponse(peerID string, message sessionWorkerEvent) {
	response := message.Response
	pending := m.pendingOperations[response.RequestID]
	if pending == nil {
		return
	}
	if pending.worker.peerID != peerID || pending.worker.token != message.Token || pending.worker.metadata.Path != message.SessionKey || pending.scope != *response.Scope {
		m.rejectOperation(response.RequestID, errors.New("Session worker returned a mismatched operation response"))
		return
	}
	var failure error
	if response.Type == "operation_error" {
		if response.Code != "" {
			failure = &chord.RemoteServiceError{Code: response.Code, Message: response.Message}
		} else {
			failure = fmt.Errorf("Session worker operation failed: %s", response.Message)
		}
	} else {
		pending.result = slices.Clone(response.Result)
	}
	m.rejectOperation(response.RequestID, failure)
}

func (m *SessionWorkerManager) handleServiceUpdate(peerID string, message sessionWorkerEvent) {
	entry := m.subscriptions[subscriptionKey(*message.Scope, message.SubscriptionID)]
	if entry == nil || entry.worker.peerID != peerID || entry.worker.token != message.Token || entry.worker.metadata.Path != message.SessionKey || entry.scope != *message.Scope {
		return
	}
	// session-worker-manager.ts:643-647 parses the update with parseServiceProviderUpdate and drops an invalid one.
	update, err := chord.ParseServiceProviderUpdate(message.Update)
	if err != nil {
		return
	}
	previous, done := entry.tail, make(chan struct{})
	entry.tail = done
	// session-worker-manager.ts:649 chains deliveryTail and no shutdown path awaits it, so a stalled listener holds only this delivery.
	m.background.Go(func() {
		defer close(done)
		<-previous
		_ = entry.publish(context.Background(), entry.subscriptionID, update)
	})
}
