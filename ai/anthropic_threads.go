package ai

// Ports packages/ai/src/api/anthropic-messages.ts (message threads: ThreadEntry, threadState, hashThreadPrefix, prepareThreadRequest, sendThreadRequest, recordThreadReply).

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// pig divergence (D352): Pi main a758f3ae4 (not in Pi 1.5.0). Message threads: the server keeps the conversation of a thread, so a request can continue from the previous reply and send only the new
// messages. PiG still builds the full request; prepareThread compares it with the previous request of the session and sends only the new
// part when nothing earlier changed. The payload is handled as the JSON the hook left, so a replacement payload is threaded like the
// provider's own request.

const messageThreadsBeta = "message-threads-2026-08-12"

// maxThreadStateEntries bounds the sessions whose thread state is kept. State is normally freed by CleanupSessionResources when a session
// ends; the cap bounds memory for library users that never call it. Dropping the least recently updated session is cheap: its thread has
// most likely expired on the server already, and otherwise its next request just starts a new thread.
// upstream: packages/ai/src/api/anthropic-messages.ts:MAX_THREAD_STATE_ENTRIES
const maxThreadStateEntries = 10_000

// anthThreadNewThreadErrors are the rejections of a continue that a new thread fixes: expired, raced, or a fixed setting changed.
var anthThreadNewThreadErrors = map[string]bool{
	"thread_not_found":            true,
	"thread_already_continued":    true,
	"thread_continue_in_flight":   true,
	"thread_fingerprint_mismatch": true,
}

// anthThreadEntry is where a session's next request can continue.
type anthThreadEntry struct {
	// prefixCount is the number of messages in the last full request as sent plus the converted reply.
	prefixCount int
	// prefixHash is hashThreadPrefix of the request's settings and those messages.
	prefixHash string
	// replyID is the id of the reply to continue from.
	replyID string
}

// anthThreadSlot is a session's state: an entry to continue from, or off after the endpoint rejected threads.
type anthThreadSlot struct {
	session string
	entry   *anthThreadEntry // nil when off
}

type anthThreadStore struct {
	mu    sync.Mutex
	slots map[string]*list.Element // value *anthThreadSlot; list order is oldest-first
	order *list.List
}

var anthThreads = &anthThreadStore{slots: map[string]*list.Element{}, order: list.New()}

func init() {
	RegisterSessionResourceCleanup(func(sessionID string) {
		anthThreads.mu.Lock()
		defer anthThreads.mu.Unlock()
		if sessionID == "" {
			clear(anthThreads.slots)
			anthThreads.order.Init()
			return
		}
		anthThreads.deleteLocked(sessionID)
	})
}

func (s *anthThreadStore) deleteLocked(session string) {
	if element, ok := s.slots[session]; ok {
		s.order.Remove(element)
		delete(s.slots, session)
	}
}

// set records a session's state, keeping at most maxThreadStateEntries sessions.
func (s *anthThreadStore) set(session string, entry *anthThreadEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteLocked(session)
	s.slots[session] = s.order.PushBack(&anthThreadSlot{session: session, entry: entry})
	if s.order.Len() > maxThreadStateEntries {
		s.deleteLocked(s.order.Front().Value.(*anthThreadSlot).session)
	}
}

// get returns the session's slot; ok is false when the session has no state.
func (s *anthThreadStore) get(session string) (slot anthThreadSlot, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	element, ok := s.slots[session]
	if !ok {
		return anthThreadSlot{}, false
	}
	return *element.Value.(*anthThreadSlot), true
}

// take removes the session's state when it still holds entry: each reply can be continued once, and removing also keeps a concurrent
// request from trying.
func (s *anthThreadStore) take(session string, entry *anthThreadEntry) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	element, ok := s.slots[session]
	if !ok || element.Value.(*anthThreadSlot).entry != entry {
		return false
	}
	s.deleteLocked(session)
	return true
}

// anthThreadPayload is the full request the hook left, split into its members and messages.
type anthThreadPayload struct {
	members  []jsonMember
	messages []json.RawMessage
}

func readAnthThreadPayload(payload any) (*anthThreadPayload, error) {
	var body []byte
	var err error
	if request, ok := payload.(anthRequest); ok {
		request.Stream = true
		body, err = marshalRequestBody(request)
	} else {
		body, err = marshalRequestBody(payload)
	}
	if err != nil {
		return nil, err
	}
	members, err := jsonSpreadMembers(body)
	if err != nil {
		return nil, err
	}
	full := &anthThreadPayload{members: members}
	for _, member := range members {
		if member.key == "messages" {
			if err := json.Unmarshal(member.value, &full.messages); err != nil {
				return nil, fmt.Errorf("anthropic: message threads need a messages array: %w", err)
			}
		}
	}
	return full, nil
}

func (full *anthThreadPayload) member(key string) json.RawMessage {
	for _, member := range full.members {
		if member.key == key {
			return member.value
		}
	}
	return json.RawMessage("null")
}

// hashPrefix is the hash of everything a continue builds on: the settings that must stay the same within a thread and the messages the
// server already holds.
//
//   - Settings, as sent: the endpoint (a thread exists on one endpoint only), and the fixed request fields. A continue omits system and
//     tools and the server keeps the thread's own, so only this check can notice a change to them, including their cache markers. The
//     server rejects changes to the other fields with thread_fingerprint_mismatch; checking them here saves that rejected request.
//   - Messages, normalized so messages that mean the same to the API hash the same.
func (full *anthThreadPayload) hashPrefix(providerID, baseURL string, messages []json.RawMessage) (string, error) {
	var settings bytes.Buffer
	provider, _ := json.Marshal(providerID)
	base, _ := json.Marshal(baseURL)
	settings.WriteString("[")
	settings.Write(provider)
	settings.WriteString(",")
	settings.Write(base)
	for _, key := range []string{"model", "system", "tools", "thinking", "output_config", "tool_choice", "betas", "fallbacks"} {
		settings.WriteString(",")
		settings.Write(full.member(key))
	}
	settings.WriteString("]")
	var conversation bytes.Buffer
	conversation.WriteString("[")
	for i, message := range messages {
		if i > 0 {
			conversation.WriteString(",")
		}
		normalized, err := normalizeThreadMessage(message)
		if err != nil {
			return "", err
		}
		conversation.Write(normalized)
	}
	conversation.WriteString("]")
	return ShortHash(settings.String() + conversation.String()), nil
}

// normalizeThreadMessage drops cache markers and writes string content as one text block.
//
// Cache markers don't change what a message means, but they move: PiG puts one on the newest message, so the message that had it in the
// last request has none in this one. Extensions may add markers anywhere too. To attach that marker, the newest user message's string
// content is sent as one text block; the next request sends the same message as a string.
func normalizeThreadMessage(message json.RawMessage) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(message))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(normalizeThreadValue(value))
}

func normalizeThreadValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		delete(v, "cache_control")
		for key, member := range v {
			if text, ok := member.(string); ok && key == "content" {
				v[key] = []any{map[string]any{"type": "text", "text": text}}
				continue
			}
			v[key] = normalizeThreadValue(member)
		}
	case []any:
		for i, member := range v {
			v[i] = normalizeThreadValue(member)
		}
	}
	return value
}

// anthThreadRequest is a request sent with a thread member.
type anthThreadRequest struct {
	session string
	// payload is the full request the hook left, which a resend without the thread sends as it is.
	payload any
	full    *anthThreadPayload
	// body is the request with its thread member.
	body json.RawMessage
	// continues reports that body sends only the messages after the previous reply.
	continues bool
}

// body writes the full request with a thread member; a continue keeps only messages[from:] and drops system and tools.
func (full *anthThreadPayload) body(thread string, from int) (json.RawMessage, error) {
	members := make([]jsonMember, 0, len(full.members)+1)
	placed := false
	for _, member := range full.members {
		switch member.key {
		case "system", "tools":
			if from > 0 {
				continue
			}
		case "messages":
			//portlint:allow nilslicenull full.messages[from:] slices a non-nil slice, so it is [] and never null when nothing follows the prefix
			messages, err := json.Marshal(full.messages[from:])
			if err != nil {
				return nil, err
			}
			member.value = messages
		case "thread":
			member.value = json.RawMessage(thread)
			placed = true
		}
		members = append(members, member)
	}
	if !placed {
		members = append(members, jsonMember{key: "thread", value: json.RawMessage(thread)})
	}
	return writeJSONObjectMembers(members)
}

func (full *anthThreadPayload) createRequest(session string, payload any) (*anthThreadRequest, error) {
	body, err := full.body(`{"type":"create"}`, 0)
	if err != nil {
		return nil, err
	}
	return &anthThreadRequest{session: session, payload: payload, full: full, body: body}, nil
}

// prepareThread builds the thread request for a session's next request: a continue when the request extends the previous one, a new thread
// otherwise. It returns nil when the endpoint rejected threads for the session, which sends the request as it is.
func (p *anthropicProvider) prepareThread(session string, payload any) (*anthThreadRequest, error) {
	slot, known := anthThreads.get(session)
	if known && slot.entry == nil {
		return nil, nil
	}
	full, err := readAnthThreadPayload(payload)
	if err != nil {
		return nil, err
	}
	if known && len(full.messages) > slot.entry.prefixCount {
		hash, err := full.hashPrefix(p.cfg.ProviderID, p.cfg.BaseURL, full.messages[:slot.entry.prefixCount])
		if err != nil {
			return nil, err
		}
		if hash == slot.entry.prefixHash && anthThreads.take(session, slot.entry) {
			//portlint:allow jsonescape the reply id is a server-issued ASCII identifier with no <, > or &
			thread, err := json.Marshal(struct {
				Type              string `json:"type"`
				PreviousMessageID string `json:"previous_message_id"`
			}{"continue", slot.entry.replyID})
			if err != nil {
				return nil, err
			}
			body, err := full.body(string(thread), slot.entry.prefixCount)
			if err != nil {
				return nil, err
			}
			return &anthThreadRequest{session: session, payload: payload, full: full, body: body, continues: true}, nil
		}
	}
	return full.createRequest(session, payload)
}

// anthThreadErrorCode is the error_code Anthropic puts in error.details of thread rejections.
func anthThreadErrorCode(body []byte) string {
	var envelope struct {
		Error struct {
			Details struct {
				ErrorCode any `json:"error_code"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	code, _ := envelope.Error.Details.ErrorCode.(string)
	return code
}

// sendThreaded sends a prepared request. A thread request the endpoint rejects about the thread (never a started stream) is resent once in
// full: as a new thread when the old one can't be continued, or without the thread member when the endpoint can't use threads, which also
// turns threads off for the session. Any other status is not about the thread (prompt too long, unknown model, strict tool limits, ...) and
// would only fail again, so the response is handed back as it is. thread is the thread request that produced the returned response, if any.
func (p *anthropicProvider) sendThreaded(ctx, requestContext context.Context, request *anthropicStreamRequest, outgoing *anthropicOutgoing, opts StreamOptions, partial *AssistantMessage) (*http.Response, *anthThreadRequest, error) {
	resp, err := p.send(ctx, requestContext, request.client, outgoing, opts)
	rejected := outgoing.thread
	if err != nil || rejected == nil || resp.StatusCode == http.StatusOK || ctx.Err() != nil {
		return resp, rejected, err
	}
	body, _ := readWebText(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	code := anthThreadErrorCode(body)
	message := anthHTTPErrorMessage(resp.StatusCode, body)
	newThread := anthThreadNewThreadErrors[code]
	// The endpoint can't use threads: it can't thread this request, or it doesn't know the thread field and rejects it without a code
	// ("thread: Extra inputs are not permitted"), e.g. with an anthropic-beta header that lacks the threads beta.
	unsupported := code == "thread_unsupported_request" || code == "" && resp.StatusCode == http.StatusBadRequest && strings.Contains(message, "thread")
	if !newThread && !unsupported {
		return resp, rejected, nil
	}
	// Recorded before the resend, so a failed resend keeps it.
	reason := code
	if reason == "" {
		reason = strconv.Itoa(resp.StatusCode)
	}
	AppendAssistantMessageDiagnostic(partial, AssistantMessageDiagnostic{
		Type:      "anthropic_thread_fallback",
		Timestamp: time.Now().UnixMilli(), //portlint:allow clock anthropic-messages.ts `timestamp: Date.now()`; the package's clock seams belong to OAuth and backoff structs, not to diagnostics
		Error:     &DiagnosticErrorInfo{Message: message},
		Details:   map[string]any{"reason": reason},
	})
	if newThread {
		created, err := rejected.full.createRequest(rejected.session, rejected.payload)
		if err != nil {
			return nil, nil, err
		}
		retry, err := p.outgoing(ctx, request.baseURL, request.client, created.body, opts, false)
		if err != nil {
			return nil, nil, err
		}
		retry.thread = created
		resp, err = p.send(ctx, requestContext, request.client, retry, opts)
		return resp, created, err
	}
	// With the threads beta, a request without a thread member is a normal request.
	retry, err := p.outgoing(ctx, request.baseURL, request.client, rejected.payload, opts, false)
	if err != nil {
		return nil, nil, err
	}
	resp, err = p.send(ctx, requestContext, request.client, retry, opts)
	if err == nil && resp.StatusCode == http.StatusOK {
		anthThreads.set(rejected.session, nil)
	}
	return resp, nil, err
}

// recordThreadReply records where the session's next request can continue: after output, the clean reply to the thread request, whose
// full request was thread.full.
func (p *anthropicProvider) recordThreadReply(model *Model, thread *anthThreadRequest, params anthropicParams, isOAuthToken bool, output *AssistantMessage) {
	// ResponseModel is set only when a fallback model answered; the server does not store those replies, so they cannot be continued.
	if output.ResponseID == "" || output.ResponseModel != "" {
		return
	}
	// Convert the reply like the next request will, keeping only the assistant message (TransformMessages adds placeholder results for
	// trailing tool calls).
	converted, err := anthConvertMessagesDetailed(TransformMessages([]Message{*output}, model, func(id string, _ *Model, _ AssistantMessage) string {
		return normalizeAnthropicToolCallID(id)
	}), isOAuthToken, params.allowEmptySignature, params.managedProvider, params.toolDefinitions)
	if err != nil {
		return
	}
	prefix := append([]json.RawMessage(nil), thread.full.messages...)
	for _, message := range converted.messages {
		if message.Role != "assistant" {
			continue
		}
		raw, err := marshalRequestBody(message)
		if err != nil {
			return
		}
		prefix = append(prefix, raw)
	}
	hash, err := thread.full.hashPrefix(p.cfg.ProviderID, p.cfg.BaseURL, prefix)
	if err != nil {
		return
	}
	anthThreads.set(thread.session, &anthThreadEntry{prefixCount: len(prefix), prefixHash: hash, replyID: output.ResponseID})
}
