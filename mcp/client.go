package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// Ports packages/mcp/src/client.ts.

const (
	defaultRequestTimeoutMs = 30_000
	maxListPages            = 1_000
)

// ClientState is the connection state of a [Client].
type ClientState string

// Connection states. A client moves idle, connecting, connected, closed and
// never back.
const (
	ClientStateIdle       ClientState = "idle"
	ClientStateConnecting ClientState = "connecting"
	ClientStateConnected  ClientState = "connected"
	ClientStateClosed     ClientState = "closed"
)

// NotificationListener receives the params of one notification method.
type NotificationListener func(params json.RawMessage)

// RequestHandler answers a request from the server. The context ends when the
// server cancels the request or the connection closes. A nil result is sent as
// an empty object.
type RequestHandler func(ctx context.Context, params json.RawMessage) (any, error)

// ClientOptions configure a [Client].
type ClientOptions struct {
	Implementation
	Capabilities    *ClientCapabilities
	ProtocolVersion string
	// RequestTimeoutMs is the default request timeout. Zero selects 30000;
	// a negative value disables the timeout. (Go mechanic: upstream tells
	// "unset" from an explicit 0 with undefined.)
	RequestTimeoutMs int
	// Roots are the roots offered to servers. A non-nil slice, even an empty
	// one, answers roots/list and declares the roots capability.
	Roots []Root
	// RootsFunc computes the roots on each roots/list request. It takes
	// precedence over Roots.
	RootsFunc func(ctx context.Context) ([]Root, error)
}

// RequestOptions are per-request options. Cancellation comes from the context
// passed to the request.
type RequestOptions struct {
	// TimeoutMs overrides the client's timeout. Zero uses the client's;
	// a negative value disables the timeout.
	TimeoutMs  int
	OnProgress func(ProgressNotification)
	// OnIssued runs once the client gave the request its id, in id order, so a caller that orders its requests can hand
	// the next one on. It does not run when the request fails before it has an id.
	OnIssued func()
}

type pendingResult struct {
	value json.RawMessage
	err   error
}

type pendingRequest struct {
	done          chan pendingResult
	timeoutMs     int
	stopTimer     func() bool
	timerGen      int
	stopAbort     func() bool
	cancellable   bool
	onProgress    func(ProgressNotification)
	progressToken string
	hasProgress   bool
}

type listenerEntry[F any] struct {
	id int
	fn F
}

// Client is an MCP client over a [Transport]. It is safe for concurrent use.
type Client struct {
	options ClientOptions

	afterFunc func(d time.Duration, f func()) (stop func() bool)

	mu               sync.Mutex
	state            ClientState
	transport        Transport
	nextRequestID    int
	lastPlaced       chan struct{} // closed once the newest request took its place on the wire; nil before the first request
	serverInfo       *Implementation
	serverCaps       *ServerCapabilities
	instructions     *string
	protocolVersion  string
	pending          map[string]*pendingRequest
	progressRequests map[string]string
	incoming         map[string]context.CancelCauseFunc
	requestHandlers  map[string]listenerEntry[RequestHandler]
	notifications    map[string][]listenerEntry[NotificationListener]
	errorListeners   []listenerEntry[func(error)]
	closeListeners   []listenerEntry[func()]
	nextListenerID   int
	disposers        []func()
	wg               sync.WaitGroup
}

// NewClient returns an idle client.
func NewClient(options ClientOptions) *Client {
	c := &Client{
		options:          options,
		state:            ClientStateIdle,
		nextRequestID:    1,
		pending:          map[string]*pendingRequest{},
		progressRequests: map[string]string{},
		incoming:         map[string]context.CancelCauseFunc{},
		requestHandlers:  map[string]listenerEntry[RequestHandler]{},
		notifications:    map[string][]listenerEntry[NotificationListener]{},
		afterFunc: func(d time.Duration, f func()) func() bool {
			return time.AfterFunc(d, f).Stop
		},
	}
	c.requestHandlers["ping"] = listenerEntry[RequestHandler]{fn: func(context.Context, json.RawMessage) (any, error) { return map[string]any{}, nil }}
	if options.RootsFunc != nil || options.Roots != nil {
		roots, rootsFunc := options.Roots, options.RootsFunc
		c.requestHandlers["roots/list"] = listenerEntry[RequestHandler]{fn: func(ctx context.Context, _ json.RawMessage) (any, error) {
			list := roots
			if rootsFunc != nil {
				var err error
				if list, err = rootsFunc(ctx); err != nil {
					return nil, err
				}
			}
			return map[string]any{"roots": append([]Root{}, list...)}, nil
		}}
	}
	return c
}

// Options returns the options the client was created with.
func (c *Client) Options() ClientOptions { return c.options }

// ConnectionState is the current state.
func (c *Client) ConnectionState() ClientState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// ServerInfo is the server's implementation, after connect.
func (c *Client) ServerInfo() *Implementation {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.serverInfo
}

// ServerCapabilities are the server's capabilities, after connect.
func (c *Client) ServerCapabilities() *ServerCapabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.serverCaps
}

// Instructions are the server's instructions, or nil.
func (c *Client) Instructions() *string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.instructions
}

// ProtocolVersion is the negotiated protocol version, or "".
func (c *Client) ProtocolVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.protocolVersion
}

func invalidError(message string) *McpError {
	return &McpError{Code: JSONRPCInvalidRequest, Message: message}
}

func objectFields(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if !isJSONObject(raw) {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, false
	}
	return fields, true
}

func isJSONString(raw json.RawMessage) bool {
	var s string
	return raw != nil && json.Unmarshal(raw, &s) == nil && len(raw) > 0 && raw[0] == '"'
}

func validateInitializeResult(raw json.RawMessage) (*InitializeResult, error) {
	fail := invalidError("Invalid MCP initialize result")
	fields, ok := objectFields(raw)
	if !ok || !isJSONString(fields["protocolVersion"]) || !isJSONObject(fields["capabilities"]) {
		return nil, fail
	}
	info, ok := objectFields(fields["serverInfo"])
	if !ok || !isJSONString(info["name"]) || !isJSONString(info["version"]) {
		return nil, fail
	}
	if instructions, present := fields["instructions"]; present && !isJSONString(instructions) {
		return nil, fail
	}
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fail
	}
	return &result, nil
}

type listPage struct {
	items      []map[string]json.RawMessage
	raws       []json.RawMessage
	nextCursor string
	hasNext    bool
}

// validateListPage checks one page of a paginated list: the items under key,
// each checked by isItem.
func validateListPage(method, key string, value json.RawMessage, isItem func(map[string]json.RawMessage) bool) (*listPage, error) {
	fields, ok := objectFields(value)
	if !ok || !isJSONArray(fields[key]) {
		return nil, invalidError("Invalid MCP " + method + " result")
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(fields[key], &raws); err != nil {
		return nil, invalidError("Invalid MCP " + method + " result")
	}
	page := &listPage{raws: raws}
	for _, raw := range raws {
		item, ok := objectFields(raw)
		if !ok || !isItem(item) {
			return nil, invalidError("Invalid entry in MCP " + method + " result")
		}
		page.items = append(page.items, item)
	}
	// Some servers end pagination with `null` or `""` instead of omitting the cursor.
	if cursor, present := fields["nextCursor"]; present && string(cursor) != "null" {
		if !isJSONString(cursor) {
			return nil, invalidError("Invalid MCP " + method + " cursor")
		}
		_ = json.Unmarshal(cursor, &page.nextCursor)
		page.hasNext = page.nextCursor != ""
	}
	return page, nil
}

func isTool(tool map[string]json.RawMessage) bool {
	return isJSONString(tool["name"]) && isJSONObject(tool["inputSchema"])
}

// isResource: `name` is required by the spec, but some servers omit it; the URI stands in.
func isResource(resource map[string]json.RawMessage) bool {
	name, present := resource["name"]
	return isJSONString(resource["uri"]) && (!present || isJSONString(name))
}

func isResourceTemplate(template map[string]json.RawMessage) bool {
	name, present := template["name"]
	return isJSONString(template["uriTemplate"]) && (!present || isJSONString(name))
}

func decodeItem[T any](raw json.RawMessage, method string) (T, error) {
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, invalidError("Invalid entry in MCP " + method + " result")
	}
	return out, nil
}

func validateReadResourceResult(raw json.RawMessage) (*ReadResourceResult, error) {
	fields, ok := objectFields(raw)
	if !ok || !isJSONArray(fields["contents"]) {
		return nil, invalidError("Invalid MCP resources/read result")
	}
	var contents []json.RawMessage
	if err := json.Unmarshal(fields["contents"], &contents); err != nil {
		return nil, invalidError("Invalid MCP resources/read result")
	}
	for _, item := range contents {
		object, ok := objectFields(item)
		if !ok || !isJSONString(object["uri"]) || (!isJSONString(object["text"]) && !isJSONString(object["blob"])) {
			return nil, invalidError("Invalid contents in MCP resources/read result")
		}
	}
	var result ReadResourceResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, invalidError("Invalid contents in MCP resources/read result")
	}
	return &result, nil
}

// validateCallToolResult: `content` is required by the spec, but servers that
// only return `structuredContent` omit it (the SDK defaults it too).
func validateCallToolResult(raw json.RawMessage) (*CallToolResult, error) {
	fail := &McpError{Code: JSONRPCInvalidRequest, Message: "Invalid MCP tools/call result"}
	fields, ok := objectFields(raw)
	if !ok {
		return nil, fail
	}
	if content, present := fields["content"]; present && !isJSONArray(content) {
		return nil, fail
	}
	if structured, present := fields["structuredContent"]; present && !isJSONObject(structured) {
		return nil, &McpError{Code: JSONRPCInvalidRequest, Message: "Invalid MCP tools/call structured content"}
	}
	var result CallToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fail
	}
	result.Raw = raw
	if result.Content == nil {
		result.Content = []ContentBlock{}
		if obj, err := orderedjson.Parse(raw); err == nil {
			obj.Set("content", json.RawMessage("[]"))
			if withContent, err := obj.MarshalJSON(); err == nil {
				result.Raw = withContent
			}
		}
	}
	return &result, nil
}

// Connect starts the transport, initializes the connection, and returns the
// server's initialize result. On failure the client is closed. The context
// bounds the wait for the server's answer; per the spec, initialize is not
// cancelled on the server.
func (c *Client) Connect(ctx context.Context, transport Transport) (*InitializeResult, error) {
	c.mu.Lock()
	if c.state != ClientStateIdle {
		state := c.state
		c.mu.Unlock()
		return nil, fmt.Errorf("Cannot connect MCP client in %s state", state)
	}
	c.state = ClientStateConnecting
	c.transport = transport
	c.disposers = []func(){
		transport.OnMessage(c.handleMessage),
		// Transport errors are reported only. Pending requests fail when the transport closes.
		transport.OnError(c.emitError),
		transport.OnClose(c.handleTransportClose),
	}
	c.mu.Unlock()

	result, err := c.initialize(ctx, transport)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	return result, nil
}

func (c *Client) initialize(ctx context.Context, transport Transport) (*InitializeResult, error) {
	if err := transport.Start(); err != nil {
		return nil, err
	}
	capabilities := ClientCapabilities{}
	if c.options.Capabilities != nil {
		capabilities = *c.options.Capabilities
	}
	if (c.options.Roots != nil || c.options.RootsFunc != nil) && capabilities.Roots == nil {
		capabilities.Roots = &ListChangedCapability{}
	}
	version := c.options.ProtocolVersion
	if version == "" {
		version = LatestProtocolVersion
	}
	raw, err := c.requestInternal(ctx, "initialize", InitializeParams{
		ProtocolVersion: version,
		Capabilities:    capabilities,
		ClientInfo:      c.options.Implementation,
	}, RequestOptions{}, true)
	if err != nil {
		return nil, err
	}
	result, err := validateInitializeResult(raw)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(SupportedProtocolVersions, result.ProtocolVersion) {
		return nil, fmt.Errorf("MCP server selected unsupported protocol version %s", result.ProtocolVersion)
	}
	c.mu.Lock()
	c.protocolVersion = result.ProtocolVersion
	c.serverInfo = &result.ServerInfo
	c.serverCaps = &result.Capabilities
	c.instructions = result.Instructions
	c.mu.Unlock()
	if setter, ok := transport.(ProtocolVersionSetter); ok {
		setter.SetProtocolVersion(result.ProtocolVersion)
	}
	if err := c.notifyInternal("notifications/initialized", nil, true); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.state == ClientStateConnecting {
		c.state = ClientStateConnected
	}
	c.mu.Unlock()
	return result, nil
}

// Request sends a request and waits for its result. It fails with
// [McpAbortError] when ctx ends first and with [McpTimeoutError] when the
// timeout passes without a response or progress notification.
func (c *Client) Request(ctx context.Context, method string, params any, options RequestOptions) (json.RawMessage, error) {
	return c.requestInternal(ctx, method, params, options, false)
}

// Notify sends a notification.
func (c *Client) Notify(method string, params any) error {
	return c.notifyInternal(method, params, false)
}

// SetRequestHandler installs the handler for a server request method. The
// returned function removes it unless a later handler replaced it.
func (c *Client) SetRequestHandler(method string, handler RequestHandler) (dispose func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextListenerID++
	id := c.nextListenerID
	c.requestHandlers[method] = listenerEntry[RequestHandler]{id, handler}
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if current, ok := c.requestHandlers[method]; ok && current.id == id {
			delete(c.requestHandlers, method)
		}
	}
}

func addListener[F any](c *Client, list *[]listenerEntry[F], fn F) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextListenerID++
	id := c.nextListenerID
	*list = append(*list, listenerEntry[F]{id, fn})
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		*list = slices.DeleteFunc(*list, func(e listenerEntry[F]) bool { return e.id == id })
	}
}

// OnNotification registers a listener for one notification method.
func (c *Client) OnNotification(method string, listener NotificationListener) (dispose func()) {
	c.mu.Lock()
	c.nextListenerID++
	id := c.nextListenerID
	c.notifications[method] = append(c.notifications[method], listenerEntry[NotificationListener]{id, listener})
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		remaining := slices.DeleteFunc(c.notifications[method], func(e listenerEntry[NotificationListener]) bool { return e.id == id })
		if len(remaining) == 0 {
			delete(c.notifications, method)
		} else {
			c.notifications[method] = remaining
		}
	}
}

// OnError registers a listener for errors that do not fail a request.
func (c *Client) OnError(listener func(error)) (dispose func()) {
	return addListener(c, &c.errorListeners, listener)
}

// OnClose registers a listener called once when the connection closes,
// whether the transport dropped or Close was called.
func (c *Client) OnClose(listener func()) (dispose func()) {
	return addListener(c, &c.closeListeners, listener)
}

// Ping sends a ping request.
func (c *Client) Ping(ctx context.Context, options RequestOptions) error {
	_, err := c.Request(ctx, "ping", nil, options)
	return err
}

// ListTools returns every tool, following nextCursor through all pages.
func (c *Client) ListTools(ctx context.Context, options RequestOptions) ([]Tool, error) {
	pages, err := c.listAll(ctx, "tools/list", "tools", isTool, options)
	if err != nil {
		return nil, err
	}
	tools := make([]Tool, 0, len(pages))
	for _, raw := range pages {
		tool, err := decodeItem[Tool](raw, "tools/list")
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

func toResource(raw json.RawMessage, item map[string]json.RawMessage) (Resource, error) {
	resource, err := decodeItem[Resource](raw, "resources/list")
	if err != nil {
		return resource, err
	}
	if _, present := item["name"]; !present {
		resource.Name = resource.URI
	}
	return resource, nil
}

func toResourceTemplate(raw json.RawMessage, item map[string]json.RawMessage) (ResourceTemplate, error) {
	template, err := decodeItem[ResourceTemplate](raw, "resources/templates/list")
	if err != nil {
		return template, err
	}
	if _, present := item["name"]; !present {
		template.Name = template.URITemplate
	}
	return template, nil
}

// ListResources returns every resource, following nextCursor through all pages.
func (c *Client) ListResources(ctx context.Context, options RequestOptions) ([]Resource, error) {
	raws, err := c.listAll(ctx, "resources/list", "resources", isResource, options)
	if err != nil {
		return nil, err
	}
	resources := make([]Resource, 0, len(raws))
	for _, raw := range raws {
		item, _ := objectFields(raw)
		resource, err := toResource(raw, item)
		if err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

// ListResourcesPage returns one page of resources, starting at cursor.
func (c *Client) ListResourcesPage(ctx context.Context, cursor *string, options RequestOptions) (*ListResourcesResult, error) {
	page, err := c.listPage(ctx, "resources/list", "resources", isResource, cursor, options)
	if err != nil {
		return nil, err
	}
	result := &ListResourcesResult{Resources: make([]Resource, 0, len(page.raws)), NextCursor: page.nextCursor}
	for i, raw := range page.raws {
		resource, err := toResource(raw, page.items[i])
		if err != nil {
			return nil, err
		}
		result.Resources = append(result.Resources, resource)
	}
	return result, nil
}

// ListResourceTemplates returns every resource template, following
// nextCursor through all pages.
func (c *Client) ListResourceTemplates(ctx context.Context, options RequestOptions) ([]ResourceTemplate, error) {
	raws, err := c.listAll(ctx, "resources/templates/list", "resourceTemplates", isResourceTemplate, options)
	if err != nil {
		return nil, err
	}
	templates := make([]ResourceTemplate, 0, len(raws))
	for _, raw := range raws {
		item, _ := objectFields(raw)
		template, err := toResourceTemplate(raw, item)
		if err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	return templates, nil
}

// ListResourceTemplatesPage returns one page of resource templates.
func (c *Client) ListResourceTemplatesPage(ctx context.Context, cursor *string, options RequestOptions) (*ListResourceTemplatesResult, error) {
	page, err := c.listPage(ctx, "resources/templates/list", "resourceTemplates", isResourceTemplate, cursor, options)
	if err != nil {
		return nil, err
	}
	result := &ListResourceTemplatesResult{ResourceTemplates: make([]ResourceTemplate, 0, len(page.raws)), NextCursor: page.nextCursor}
	for i, raw := range page.raws {
		template, err := toResourceTemplate(raw, page.items[i])
		if err != nil {
			return nil, err
		}
		result.ResourceTemplates = append(result.ResourceTemplates, template)
	}
	return result, nil
}

// ReadResource reads a resource.
func (c *Client) ReadResource(ctx context.Context, uri string, options RequestOptions) (*ReadResourceResult, error) {
	raw, err := c.Request(ctx, "resources/read", map[string]any{"uri": uri}, options)
	if err != nil {
		return nil, err
	}
	return validateReadResourceResult(raw)
}

func (c *Client) listPage(ctx context.Context, method, key string, isItem func(map[string]json.RawMessage) bool, cursor *string, options RequestOptions) (*listPage, error) {
	var params any
	if cursor != nil {
		params = map[string]any{"cursor": *cursor}
	}
	raw, err := c.Request(ctx, method, params, options)
	if err != nil {
		return nil, err
	}
	return validateListPage(method, key, raw, isItem)
}

// listAll returns every raw item of a paginated list method.
func (c *Client) listAll(ctx context.Context, method, key string, isItem func(map[string]json.RawMessage) bool, options RequestOptions) ([]json.RawMessage, error) {
	var items []json.RawMessage
	cursors := map[string]bool{}
	var cursor *string
	for range maxListPages {
		page, err := c.listPage(ctx, method, key, isItem, cursor, options)
		if err != nil {
			return nil, err
		}
		items = append(items, page.raws...)
		if !page.hasNext {
			return items, nil
		}
		if cursors[page.nextCursor] {
			return nil, fmt.Errorf("MCP %s returned duplicate cursor: %s", method, page.nextCursor)
		}
		cursors[page.nextCursor] = true
		next := page.nextCursor
		cursor = &next
	}
	return nil, fmt.Errorf("MCP %s exceeded %d pages", method, maxListPages)
}

// CallTool calls a tool. A nil args omits the arguments member; args may be a
// map, a struct, or json.RawMessage.
func (c *Client) CallTool(ctx context.Context, name string, args any, options RequestOptions) (*CallToolResult, error) {
	params := orderedjson.New()
	_ = params.SetValue("name", name)
	if args != nil {
		if raw, ok := args.(json.RawMessage); ok && raw == nil {
			args = nil
		}
	}
	if args != nil {
		if err := params.SetValue("arguments", args); err != nil {
			return nil, err
		}
	}
	raw, err := c.Request(ctx, "tools/call", params, options)
	if err != nil {
		return nil, err
	}
	return validateCallToolResult(raw)
}

// Close rejects in-flight requests, closes the transport, and waits for the
// goroutines the client started for server requests.
func (c *Client) Close() error {
	c.mu.Lock()
	transport := c.transport
	c.transport = nil
	disposers := c.disposers
	c.disposers = nil
	c.mu.Unlock()
	for _, dispose := range disposers {
		dispose()
	}
	c.markClosed(NewConnectionClosedError())
	var err error
	if transport != nil {
		err = transport.Close()
	}
	c.wg.Wait()
	return err
}

func abortReason(ctx context.Context) string {
	cause := context.Cause(ctx)
	switch {
	case cause == nil:
		return "Aborted"
	case errors.Is(cause, context.Canceled):
		return "AbortError: This operation was aborted"
	case errors.Is(cause, context.DeadlineExceeded):
		return "TimeoutError: The operation was aborted due to timeout"
	}
	return cause.Error()
}

func (c *Client) requestInternal(ctx context.Context, method string, params any, options RequestOptions, allowConnecting bool) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	transport, err := c.requireTransportLocked(allowConnecting)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if ctx.Err() != nil {
		c.mu.Unlock()
		return nil, &McpAbortError{}
	}
	id := c.nextRequestID
	c.nextRequestID++
	// A request is written after the one with the previous id (see OrderedSender), as Pi's transport.send calls run in
	// request order.
	previousPlaced := c.lastPlaced
	if previousPlaced == nil {
		previousPlaced = closedChan
	}
	placed := make(chan struct{})
	c.lastPlaced = placed
	c.mu.Unlock()
	var placeOnce sync.Once
	place := func() { placeOnce.Do(func() { close(placed) }) }
	sending := false
	defer func() {
		if !sending {
			place()
		}
	}()
	if options.OnIssued != nil {
		options.OnIssued()
	}

	requestID := NumberID(float64(id))
	key := requestID.key()
	rawParams, err := requestParams(params, options.OnProgress != nil, requestID)
	if err != nil {
		return nil, err
	}
	message := NewRequest(requestID, method, rawParams)

	timeoutMs := options.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = c.options.RequestTimeoutMs
	}
	if timeoutMs == 0 {
		timeoutMs = defaultRequestTimeoutMs
	}
	entry := &pendingRequest{
		done:        make(chan pendingResult, 1),
		timeoutMs:   timeoutMs,
		cancellable: method != "initialize",
		onProgress:  options.OnProgress,
	}
	if options.OnProgress != nil {
		entry.hasProgress = true
		entry.progressToken = key
	}

	c.mu.Lock()
	if _, err := c.requireTransportLocked(allowConnecting); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	c.pending[key] = entry
	if entry.hasProgress {
		c.progressRequests[entry.progressToken] = key
	}
	c.armTimeoutLocked(key, entry)
	// The spec forbids cancelling `initialize`. The callback needs c.mu, so it
	// cannot run before the request is registered.
	entry.stopAbort = context.AfterFunc(ctx, func() {
		c.cancelPending(key, &McpAbortError{}, method != "initialize", abortReason(ctx))
	})
	c.wg.Add(1)
	c.mu.Unlock()

	sending = true
	go func() {
		defer c.wg.Done()
		defer place()
		<-previousPlaced
		var err error
		if ordered, ok := transport.(OrderedSender); ok {
			err = ordered.SendOrdered(message, place)
		} else {
			place()
			err = transport.Send(message)
		}
		if err != nil {
			c.cancelPending(key, err, false, "")
		}
	}()

	result := <-entry.done
	return result.value, result.err
}

func requestParams(params any, withProgress bool, id JSONRPCID) (json.RawMessage, error) {
	var raw json.RawMessage
	switch p := params.(type) {
	case nil:
	case json.RawMessage:
		raw = p
	case *orderedjson.Object:
		data, err := p.MarshalJSON()
		if err != nil {
			return nil, err
		}
		raw = data
	default:
		data, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = data
	}
	if !withProgress {
		return raw, nil
	}
	obj := orderedjson.New()
	if raw != nil {
		if parsed, err := orderedjson.Parse(raw); err == nil {
			obj = parsed
		}
	}
	meta := orderedjson.New()
	if existing, ok := obj.Get("_meta"); ok {
		if parsed, err := orderedjson.Parse(existing); err == nil {
			meta = parsed
		}
	}
	if err := meta.SetValue("progressToken", id); err != nil {
		return nil, err
	}
	metaRaw, _ := meta.MarshalJSON()
	obj.Set("_meta", metaRaw)
	return obj.MarshalJSON()
}

func (c *Client) notifyInternal(method string, params any, allowConnecting bool) error {
	c.mu.Lock()
	transport, err := c.requireTransportLocked(allowConnecting)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	raw, err := requestParams(params, false, JSONRPCID{})
	if err != nil {
		return err
	}
	return transport.Send(NewNotification(method, raw))
}

func (c *Client) requireTransportLocked(allowConnecting bool) (Transport, error) {
	if c.transport != nil && (c.state == ClientStateConnected || (allowConnecting && c.state == ClientStateConnecting)) {
		return c.transport, nil
	}
	return nil, &McpConnectionClosedError{Message: fmt.Sprintf("MCP client is %s", c.state)}
}

func (c *Client) handleMessage(message JSONRPCMessage) {
	switch {
	case message.IsResponse():
		c.handleResponse(message)
	case message.IsRequest():
		c.startRequest(message)
	case message.IsNotification():
		c.handleNotification(message.Method, message.Params)
	default:
		c.emitError(&McpError{Code: JSONRPCInvalidRequest, Message: "Received invalid JSON-RPC message"})
	}
}

func (c *Client) handleResponse(message JSONRPCMessage) {
	key := message.ID.key()
	c.mu.Lock()
	entry := c.pending[key]
	if entry == nil {
		c.mu.Unlock()
		c.emitError(fmt.Errorf("Received response for unknown MCP request %s", message.ID.String()))
		return
	}
	c.removePendingLocked(key, entry)
	c.mu.Unlock()
	if message.Error != nil {
		entry.done <- pendingResult{err: &McpError{Code: message.Error.Code, Message: message.Error.Message, Data: message.Error.Data}}
	} else {
		entry.done <- pendingResult{value: message.Result}
	}
}

// startRequest serves a server request on a goroutine the client owns and
// drains in Close. The request's context is registered before the next
// message is handled, so a following notifications/cancelled finds it.
func (c *Client) startRequest(message JSONRPCMessage) {
	c.mu.Lock()
	transport := c.transport
	if transport == nil {
		c.mu.Unlock()
		return
	}
	handler := c.requestHandlers[message.Method].fn
	key := message.ID.key()
	var ctx context.Context
	if handler != nil {
		var cancel context.CancelCauseFunc
		ctx, cancel = context.WithCancelCause(context.Background())
		c.incoming[key] = cancel
	}
	c.wg.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.wg.Done()
		c.serveRequest(ctx, transport, handler, key, message)
	}()
}

func (c *Client) serveRequest(ctx context.Context, transport Transport, handler RequestHandler, key string, message JSONRPCMessage) {
	if handler == nil {
		if err := transport.Send(NewErrorResponse(*message.ID, JSONRPCMethodNotFound, "Method not found: "+message.Method, nil)); err != nil {
			c.emitError(err)
		}
		return
	}
	defer func() {
		c.mu.Lock()
		if cancel := c.incoming[key]; cancel != nil {
			cancel(nil)
			delete(c.incoming, key)
		}
		c.mu.Unlock()
	}()
	result, err := runHandler(ctx, handler, message.Params)
	var response JSONRPCMessage
	if err != nil {
		if mcpErr, ok := errors.AsType[*McpError](err); ok {
			response = NewErrorResponse(*message.ID, mcpErr.Code, mcpErr.Message, mcpErr.Data)
		} else {
			response = NewErrorResponse(*message.ID, JSONRPCInternalError, err.Error(), nil)
		}
	} else {
		if result == nil {
			result = map[string]any{}
		}
		raw, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			response = NewErrorResponse(*message.ID, JSONRPCInternalError, marshalErr.Error(), nil)
		} else {
			response = NewResult(*message.ID, raw)
		}
	}
	if err := transport.Send(response); err != nil {
		c.emitError(err)
	}
}

func runHandler(ctx context.Context, handler RequestHandler, params json.RawMessage) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	return handler(ctx, params)
}

func (c *Client) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "notifications/progress":
		c.handleProgress(params)
	case "notifications/cancelled":
		c.handleCancelled(params)
	}
	c.mu.Lock()
	listeners := append([]listenerEntry[NotificationListener](nil), c.notifications[method]...)
	c.mu.Unlock()
	for _, l := range listeners {
		if err := callListener(func() { l.fn(params) }); err != nil {
			c.emitError(err)
		}
	}
}

func callListener(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", r)
			}
		}
	}()
	f()
	return nil
}

func (c *Client) handleProgress(params json.RawMessage) {
	fields, ok := objectFields(params)
	if !ok {
		return
	}
	var token JSONRPCID
	var progress float64
	if token.UnmarshalJSON(fields["progressToken"]) != nil || json.Unmarshal(fields["progress"], &progress) != nil || !isNumber(fields["progress"]) {
		return
	}
	c.mu.Lock()
	requestKey, ok := c.progressRequests[token.key()]
	entry := c.pending[requestKey]
	if !ok || entry == nil {
		c.mu.Unlock()
		return
	}
	c.armTimeoutLocked(requestKey, entry)
	onProgress := entry.onProgress
	c.mu.Unlock()
	if onProgress == nil {
		return
	}
	var notification ProgressNotification
	if json.Unmarshal(params, &notification) != nil {
		notification = ProgressNotification{ProgressToken: token, Progress: progress}
	}
	if err := callListener(func() { onProgress(notification) }); err != nil {
		c.emitError(err)
	}
}

func isNumber(raw json.RawMessage) bool {
	var f float64
	return len(raw) > 0 && raw[0] != '"' && json.Unmarshal(raw, &f) == nil && !math.IsNaN(f)
}

func (c *Client) handleCancelled(params json.RawMessage) {
	fields, ok := objectFields(params)
	if !ok {
		return
	}
	var id JSONRPCID
	if id.UnmarshalJSON(fields["requestId"]) != nil {
		return
	}
	var reason string
	if raw, present := fields["reason"]; present {
		if isJSONString(raw) {
			_ = json.Unmarshal(raw, &reason)
		} else {
			reason = string(raw)
		}
	}
	c.mu.Lock()
	cancel := c.incoming[id.key()]
	c.mu.Unlock()
	if cancel != nil {
		cancel(errors.New(reason))
	}
}

// armTimeoutLocked (re)starts the request's timer. Progress notifications
// renew it.
func (c *Client) armTimeoutLocked(key string, entry *pendingRequest) {
	if entry.stopTimer != nil {
		entry.stopTimer()
		entry.stopTimer = nil
	}
	entry.timerGen++
	if entry.timeoutMs <= 0 {
		return
	}
	timeoutMs, gen := entry.timeoutMs, entry.timerGen
	entry.stopTimer = c.afterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
		c.mu.Lock()
		current := c.pending[key]
		renewed := entry.timerGen != gen
		c.mu.Unlock()
		if current != entry || renewed {
			return
		}
		c.cancelPending(key, &McpTimeoutError{TimeoutMs: timeoutMs}, entry.cancellable, "Request timed out")
	})
}

func (c *Client) cancelPending(key string, err error, notifyServer bool, reason string) {
	c.mu.Lock()
	entry := c.pending[key]
	if entry == nil {
		c.mu.Unlock()
		return
	}
	c.removePendingLocked(key, entry)
	transport := c.transport
	notify := notifyServer && transport != nil
	if notify {
		c.wg.Add(1)
	}
	c.mu.Unlock()
	entry.done <- pendingResult{err: err}
	if notify {
		id := requestIDFromKey(key)
		params := map[string]any{"requestId": id}
		if reason != "" {
			params["reason"] = reason
		}
		raw, _ := json.Marshal(params)
		go func() {
			defer c.wg.Done()
			if sendErr := transport.Send(NewNotification("notifications/cancelled", raw)); sendErr != nil {
				c.emitError(sendErr)
			}
		}()
	}
}

func requestIDFromKey(key string) JSONRPCID {
	if len(key) > 2 && key[0] == 'n' {
		n, _ := strconv.ParseFloat(key[2:], 64)
		return NumberID(n)
	}
	return StringID(key[2:])
}

func (c *Client) removePendingLocked(key string, entry *pendingRequest) {
	delete(c.pending, key)
	if entry.stopTimer != nil {
		entry.stopTimer()
	}
	if entry.hasProgress {
		delete(c.progressRequests, entry.progressToken)
	}
	if entry.stopAbort != nil {
		entry.stopAbort()
	}
}

func (c *Client) handleTransportClose() {
	c.markClosed(NewConnectionClosedError())
}

// markClosed is idempotent: it rejects in-flight requests, aborts server
// requests being served, flips the state, and notifies the close listeners
// once.
func (c *Client) markClosed(err error) {
	c.mu.Lock()
	wasClosed := c.state == ClientStateClosed
	c.state = ClientStateClosed
	entries := c.pending
	c.pending = map[string]*pendingRequest{}
	c.progressRequests = map[string]string{}
	for _, entry := range entries {
		if entry.stopTimer != nil {
			entry.stopTimer()
		}
		if entry.stopAbort != nil {
			entry.stopAbort()
		}
	}
	incoming := c.incoming
	c.incoming = map[string]context.CancelCauseFunc{}
	var listeners []listenerEntry[func()]
	if !wasClosed {
		listeners = append(listeners, c.closeListeners...)
	}
	c.mu.Unlock()
	for _, entry := range entries {
		entry.done <- pendingResult{err: err}
	}
	for _, cancel := range incoming {
		cancel(err)
	}
	for _, l := range listeners {
		if listenerErr := callListener(l.fn); listenerErr != nil {
			c.emitError(listenerErr)
		}
	}
}

func (c *Client) emitError(err error) {
	c.mu.Lock()
	listeners := append([]listenerEntry[func(error)](nil), c.errorListeners...)
	c.mu.Unlock()
	for _, l := range listeners {
		l.fn(err)
	}
}

// closedChan is a channel that is already closed.
var closedChan = func() chan struct{} { c := make(chan struct{}); close(c); return c }()
