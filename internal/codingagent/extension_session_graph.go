package codingagent

// Ports packages/coding-agent/src/core/session-manager.ts

// The SessionManager an Extension SDK constructs. Pi's class is a value an extension builds and passes around (`SessionManager.inMemory()`, `SessionManager.open(path)`, `SessionManager.list(cwd)`); the Go, Rust and Python SDKs hold a handle and the host holds the object: a *Session, the same append-only log the runtime itself uses, built by the same constructors the runtime uses. The Node runtime serves Pi's own class in the extension process.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// GraphArgs are the positional arguments of a session graph call: a JSON array, which may stop early, whose members may be null for Pi's undefined.
type GraphArgs []json.RawMessage

// ParseGraphArgs decodes the positional arguments of a session graph call.
func ParseGraphArgs(raw json.RawMessage) (GraphArgs, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var args []json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("session graph arguments are not a JSON array: %w", err)
	}
	return args, nil
}

// Raw is argument i, nil when Pi's undefined or null.
func (a GraphArgs) Raw(i int) json.RawMessage {
	if i >= len(a) || string(a[i]) == "null" {
		return nil
	}
	return a[i]
}

// Has reports whether argument i is neither undefined nor null.
func (a GraphArgs) Has(i int) bool { return a.Raw(i) != nil }

// String is argument i, which must be a string. Pi's undefined is the empty string; use Has to tell it from "".
func (a GraphArgs) String(i int) (string, error) {
	raw := a.Raw(i)
	if raw == nil {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("argument %d is not a string", i+1)
	}
	return value, nil
}

// OptString is argument i as a pointer, nil when undefined or null.
func (a GraphArgs) OptString(i int) (*string, error) {
	if !a.Has(i) {
		return nil, nil
	}
	value, err := a.String(i)
	return &value, err
}

// Bool is argument i, false when undefined.
func (a GraphArgs) Bool(i int) (bool, error) {
	raw := a.Raw(i)
	if raw == nil {
		return false, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("argument %d is not a boolean", i+1)
	}
	return value, nil
}

// Int is argument i, 0 when undefined.
func (a GraphArgs) Int(i int) (int, error) {
	raw := a.Raw(i)
	if raw == nil {
		return 0, nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("argument %d is not a number", i+1)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value > math.MaxInt32 || value < math.MinInt32 {
		return 0, fmt.Errorf("argument %d is not an integer in range", i+1)
	}
	return int(value), nil //portlint:allow numbers value is finite and within the int32 range here
}

// Unmarshal runs decode on argument i, with undefined and null left alone, and reports its error as the argument's.
func (a GraphArgs) Unmarshal(i int, decode func(json.RawMessage) error) error {
	raw := a.Raw(i)
	if raw == nil {
		return nil
	}
	if err := decode(raw); err != nil {
		return fmt.Errorf("argument %d: %w", i+1, err)
	}
	return nil
}

// GraphSessionManager is a SessionManager an extension constructed.
type GraphSessionManager struct {
	session *Session
}

// Session is the log the object holds. A session created from the object shares it, as Pi's createAgentSession shares the SessionManager it is given.
func (g *GraphSessionManager) Session() *Session { return g.session }

// graphNewSessionOptions is Pi's NewSessionOptions.
type graphNewSessionOptions struct {
	ID            *string `json:"id"`
	ParentSession string  `json:"parentSession"`
}

// graphSessionDir is Pi's `sessionDir ? normalizePath(sessionDir) : getDefaultSessionDir(cwd)`.
func graphSessionDir(cwd string, sessionDir *string) (string, error) {
	if sessionDir != nil && *sessionDir != "" {
		return ResolvePath(*sessionDir, "")
	}
	return GetDefaultSessionDirPath(cwd, AgentDir()), nil
}

// NewGraphSessionManager builds the SessionManager of Pi's static factory op (create, open, continueRecent, inMemory, forkFrom).
func NewGraphSessionManager(op string, raw json.RawMessage) (*GraphSessionManager, error) {
	args, err := ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	switch op {
	case "create":
		// create(cwd, sessionDir?, options?)
		cwd, err := args.String(0)
		if err != nil {
			return nil, err
		}
		sessionDir, err := args.OptString(1)
		if err != nil {
			return nil, err
		}
		var options graphNewSessionOptions
		if err := args.Unmarshal(2, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
			return nil, err
		}
		dir, err := graphSessionDir(cwd, sessionDir)
		if err != nil {
			return nil, err
		}
		id := ""
		if options.ID != nil {
			id = *options.ID
		}
		session, err := NewSessionManagerWithDir(cwd, dir).Create(id, options.ParentSession)
		if err != nil {
			return nil, err
		}
		return &GraphSessionManager{session}, nil
	case "open":
		// open(path, sessionDir?, cwdOverride?)
		path, err := args.String(0)
		if err != nil {
			return nil, err
		}
		sessionDir, err := args.OptString(1)
		if err != nil {
			return nil, err
		}
		cwdOverride, err := args.OptString(2)
		if err != nil {
			return nil, err
		}
		return openGraphSession(path, sessionDir, cwdOverride)
	case "continueRecent":
		// continueRecent(cwd, sessionDir?)
		cwd, err := args.String(0)
		if err != nil {
			return nil, err
		}
		sessionDir, err := args.OptString(1)
		if err != nil {
			return nil, err
		}
		dir, err := graphSessionDir(cwd, sessionDir)
		if err != nil {
			return nil, err
		}
		manager := NewSessionManagerWithDir(cwd, dir)
		var session *Session
		if recent := manager.findMostRecent(sessionDir != nil && *sessionDir != "" && !manager.sessionDirIsCwdScoped()); recent != "" {
			session, err = manager.Load(recent)
		} else {
			session, err = manager.Create("", "")
		}
		if err != nil {
			return nil, err
		}
		session.effectiveCWD = new(managerCWD(cwd))
		return &GraphSessionManager{session}, nil
	case "inMemory":
		// inMemory(cwd = process.cwd(), options?, entries?)
		cwd, err := args.String(0)
		if err != nil {
			return nil, err
		}
		if cwd == "" {
			if cwd, err = os.Getwd(); err != nil {
				return nil, err
			}
		}
		var options graphNewSessionOptions
		if err := args.Unmarshal(1, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
			return nil, err
		}
		var entries []json.RawMessage
		if err := args.Unmarshal(2, func(raw json.RawMessage) error { return json.Unmarshal(raw, &entries) }); err != nil {
			return nil, err
		}
		resolved := managerCWD(cwd)
		var session *Session
		if len(entries) > 0 {
			records := make([]FileEntry, len(entries))
			for i, entry := range entries {
				records[i] = sessionentry.DecodeFileEntry(entry)
			}
			id := ""
			if options.ID != nil {
				id = *options.ID
			}
			if session, err = newSessionFromEntries(resolved, id, records); err != nil {
				return nil, err
			}
			session.header.ParentSession = firstNonEmpty(session.header.ParentSession, options.ParentSession)
		} else if session, err = newSessionWithOptions(resolved, options.ID, options.ParentSession); err != nil {
			return nil, err
		}
		return &GraphSessionManager{session}, nil
	case "forkFrom":
		// forkFrom(sourcePath, targetCwd, sessionDir?, options?)
		source, err := args.String(0)
		if err != nil {
			return nil, err
		}
		targetCwd, err := args.String(1)
		if err != nil {
			return nil, err
		}
		sessionDir, err := args.OptString(2)
		if err != nil {
			return nil, err
		}
		var options graphNewSessionOptions
		if err := args.Unmarshal(3, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
			return nil, err
		}
		resolvedTarget, err := ResolvePath(targetCwd, "")
		if err != nil {
			return nil, err
		}
		dir, err := graphSessionDir(resolvedTarget, sessionDir)
		if err != nil {
			return nil, err
		}
		manager := NewSessionManagerWithDir(resolvedTarget, dir)
		var forked *Session
		if options.ID != nil {
			forked, err = manager.ForkFromFile(source, *options.ID)
		} else {
			forked, err = manager.ForkFromFile(source)
		}
		if err != nil {
			return nil, err
		}
		forked.effectiveCWD = new(managerCWD(resolvedTarget))
		forked.sessionDir = dir
		return &GraphSessionManager{forked}, nil
	}
	return nil, fmt.Errorf("SessionManager has no static factory %q", op)
}

// openGraphSession is SessionManager.open: the session file at path, in sessionDir (default: the file's directory) with cwdOverride or the header's cwd (default: the process's).
func openGraphSession(path string, sessionDir, cwdOverride *string) (*GraphSessionManager, error) {
	resolved, err := ResolvePath(path, "")
	if err != nil {
		return nil, err
	}
	cwd := ""
	if cwdOverride != nil {
		cwd = *cwdOverride
	} else if header := readSessionHeaderForDiscovery(resolved); header != nil && header.CWD != "" {
		cwd = header.CWD
	}
	if cwd == "" {
		if cwd, err = os.Getwd(); err != nil {
			return nil, err
		}
	}
	dir := filepath.Dir(resolved)
	if sessionDir != nil && *sessionDir != "" {
		if dir, err = ResolvePath(*sessionDir, ""); err != nil {
			return nil, err
		}
	}
	session, err := NewSessionManagerWithDir(cwd, dir).Load(resolved)
	if err != nil {
		return nil, err
	}
	session.effectiveCWD = new(managerCWD(cwd))
	return &GraphSessionManager{session}, nil
}

// GraphSessionManagerStatic runs the static function of Pi's SessionManager that constructs no object.
func GraphSessionManagerStatic(ctx context.Context, scope subprocess.SessionGraphScope, method string, raw json.RawMessage) (any, error) {
	args, err := ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	switch method {
	case "findById":
		// findById(cwd, id, sessionDir?)
		cwd, err := args.String(0)
		if err != nil {
			return nil, err
		}
		id, err := args.String(1)
		if err != nil {
			return nil, err
		}
		sessionDir, err := args.OptString(2)
		if err != nil {
			return nil, err
		}
		dir, err := graphSessionDir(cwd, sessionDir)
		if err != nil {
			return nil, err
		}
		if found := NewSessionManagerWithDir(cwd, dir).FindByID(id); found != "" {
			return found, nil
		}
		return nil, nil
	case "list":
		// list(cwd, sessionDir?, onProgress?, signal?)
		cwd, err := args.String(0)
		if err != nil {
			return nil, err
		}
		sessionDir, err := args.OptString(1)
		if err != nil {
			return nil, err
		}
		dir, err := graphSessionDir(cwd, sessionDir)
		if err != nil {
			return nil, err
		}
		progress, err := graphListProgress(ctx, scope, args, 2)
		if err != nil {
			return nil, err
		}
		infos, err := NewSessionManagerWithDir(cwd, dir).ListCurrentSessions(SessionListOptions{Context: ctx, OnProgress: progress})
		if err != nil {
			return nil, err
		}
		return graphSessionInfos(infos), nil
	case "listAll":
		// listAll(sessionDir?, onProgress?, signal?)
		sessionDir, err := args.OptString(0)
		if err != nil {
			return nil, err
		}
		progress, err := graphListProgress(ctx, scope, args, 1)
		if err != nil {
			return nil, err
		}
		var infos []SessionInfo
		if sessionDir != nil && *sessionDir != "" {
			dir, err := ResolvePath(*sessionDir, "")
			if err != nil {
				return nil, err
			}
			infos, err = listSessionsInDirWithOptions(dir, SessionListOptions{Context: ctx, OnProgress: progress})
			if err != nil {
				return nil, err
			}
		} else {
			infos, err = NewSessionManagerWithDir(".", GetDefaultSessionDirPath(".", AgentDir())).ListAllSessions(SessionListOptions{Context: ctx, OnProgress: progress})
			if err != nil {
				return nil, err
			}
		}
		return graphSessionInfos(infos), nil
	}
	return nil, fmt.Errorf("SessionManager has no static function %q", method)
}

// graphSessionInfos is Pi's SessionInfo[] as JSON: the dates are what Date.toISOString writes, and name and parentSessionPath are absent when Pi leaves them undefined.
func graphSessionInfos(infos []SessionInfo) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(infos))
	for _, info := range infos {
		object := orderedjson.New()
		set := func(key string, value any) {
			encoded, _ := json.Marshal(value)
			object.Set(key, encoded)
		}
		set("path", info.Path)
		set("id", info.ID)
		set("cwd", info.CWD)
		if info.Name != "" {
			set("name", info.Name)
		}
		if info.ParentSessionPath != "" {
			set("parentSessionPath", info.ParentSessionPath)
		}
		set("created", info.Created.UTC().Format("2006-01-02T15:04:05.000Z"))
		set("modified", info.Modified.UTC().Format("2006-01-02T15:04:05.000Z"))
		set("messageCount", info.MessageCount)
		set("firstMessage", info.FirstMessage)
		set("allMessagesText", info.AllMessagesText)
		encoded, _ := object.MarshalJSON()
		out = append(out, encoded)
	}
	return out
}

// graphReadMethods are the reads ExtensionSessionRead answers, by the argument names it takes.
var graphReadMethods = map[string][]string{
	"getCwd": nil, "getSessionDir": nil, "isPersisted": nil, "usesDefaultSessionDir": nil, "getSessionId": nil, "getSessionFile": nil, "getSessionName": nil,
	"getLeafId": nil, "getHeader": nil, "getEntries": nil, "getLeafEntry": nil, "getTree": nil, "buildContextEntries": nil, "buildSessionProjection": nil, "buildSessionContext": nil,
	"getEntry": {"id"}, "getLabel": {"id"}, "getChildren": {"parentId"}, "getBranch": {"fromId"},
}

// Call runs a SessionManager method.
func (g *GraphSessionManager) Call(_ context.Context, method string, raw json.RawMessage) (any, error) {
	args, err := ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	session := g.session
	if names, read := graphReadMethods[method]; read {
		params := map[string]any{}
		for i, name := range names {
			if value := args.Raw(i); value != nil {
				params[name] = value
			}
		}
		//portlint:allow mapkeyorder ExtensionSessionRead decodes these arguments by method and member name; the encoding is read back at once and no one observes its member order
		encoded, _ := json.Marshal(params)
		return ExtensionSessionRead(ExtensionSessionView{Session: session, CWD: session.CWD(), SessionDir: session.GetSessionDir()}, method, encoded)
	}
	switch method {
	case "getEntryCount":
		return session.GetEntryCount(), nil
	case "appendMessage":
		var message agent.AgentMessage
		if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &message) }); err != nil {
			return nil, err
		}
		return session.AppendMessage(message)
	case "appendThinkingLevelChange":
		level, err := args.String(0)
		if err != nil {
			return nil, err
		}
		return session.AppendThinkingLevelChange(level)
	case "appendModelChange":
		provider, err := args.String(0)
		if err != nil {
			return nil, err
		}
		modelID, err := args.String(1)
		if err != nil {
			return nil, err
		}
		return session.AppendModelChange(provider, modelID)
	case "appendUsage":
		// appendUsage(kind, provider, model, usage, note?)
		kind, _ := args.String(0)
		provider, _ := args.String(1)
		model, _ := args.String(2)
		var usage ai.Usage
		if err := args.Unmarshal(3, func(raw json.RawMessage) error { return json.Unmarshal(raw, &usage) }); err != nil {
			return nil, err
		}
		note, _ := args.String(4)
		entry, err := session.AppendUsage(kind, provider, model, usage, note)
		if err != nil {
			return nil, err
		}
		return graphEntry(entry)
	case "appendCompaction":
		// appendCompaction(summary, firstKeptEntryId, tokensBefore, details?, fromHook?, usage?)
		summary, _ := args.String(0)
		firstKept, _ := args.String(1)
		tokensBefore, err := args.Int(2)
		if err != nil {
			return nil, err
		}
		var details any
		details = orderedjson.Value(args.Raw(3))
		fromHook, _ := args.Bool(4)
		fromHookUndefined := !args.Has(4)
		var usage *ai.Usage
		if raw := args.Raw(5); raw != nil {
			usage = new(ai.Usage)
			if err := json.Unmarshal(raw, usage); err != nil {
				return nil, err
			}
		}
		if fromHookUndefined {
			return session.AppendCompactionHookUndefined(summary, firstKept, tokensBefore, details, usage)
		}
		return session.AppendCompaction(summary, firstKept, tokensBefore, details, fromHook, usage)
	case "appendCustomEntry":
		customType, err := args.String(0)
		if err != nil {
			return nil, err
		}
		var data any
		data = orderedjson.Value(args.Raw(1))
		return session.AppendCustomEntry(customType, data)
	case "appendSessionInfo":
		name, err := args.String(0)
		if err != nil {
			return nil, err
		}
		return session.AppendSessionInfo(name)
	case "appendCustomMessageEntry":
		// appendCustomMessageEntry(customType, content, display, details?)
		customType, err := args.String(0)
		if err != nil {
			return nil, err
		}
		var content, details any
		content = orderedjson.Value(args.Raw(1))
		display, _ := args.Bool(2)
		details = orderedjson.Value(args.Raw(3))
		return session.AppendCustomMessageEntry(customType, content, display, details)
	case "appendContextEdit":
		targetID, err := args.String(0)
		if err != nil {
			return nil, err
		}
		if !args.Has(1) {
			// A null replacement removes the content from the model context.
			return session.AppendContextEdit(targetID, nil)
		}
		var replacement ContextEditReplacement
		if err := args.Unmarshal(1, func(raw json.RawMessage) error { return json.Unmarshal(raw, &replacement) }); err != nil {
			return nil, err
		}
		return session.AppendContextEdit(targetID, &replacement)
	case "appendLabelChange":
		targetID, err := args.String(0)
		if err != nil {
			return nil, err
		}
		label, err := args.OptString(1)
		if err != nil {
			return nil, err
		}
		return session.AppendLabelChange(targetID, label)
	case "branch":
		id, err := args.String(0)
		if err != nil {
			return nil, err
		}
		return nil, session.Branch(id)
	case "resetLeaf":
		session.ResetLeaf()
		return nil, nil
	case "branchWithSummary":
		// branchWithSummary(branchFromId, summary, details?, fromHook?, usage?)
		parent, err := args.OptString(0)
		if err != nil {
			return nil, err
		}
		summary, _ := args.String(1)
		var details any
		details = orderedjson.Value(args.Raw(2))
		fromHook, _ := args.Bool(3)
		var usage *ai.Usage
		if raw := args.Raw(4); raw != nil {
			usage = new(ai.Usage)
			if err := json.Unmarshal(raw, usage); err != nil {
				return nil, err
			}
		}
		return session.BranchWithSummary(parent, summary, details, fromHook, usage)
	case "createBranchedSession":
		leafID, err := args.String(0)
		if err != nil {
			return nil, err
		}
		path, err := session.CreateBranchedSession(leafID)
		if err != nil {
			return nil, err
		}
		if path == "" {
			return nil, nil
		}
		return path, nil
	case "newSession":
		var options graphNewSessionOptions
		if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
			return nil, err
		}
		path, err := session.NewSession(&NewSessionOptions{ID: options.ID, ParentSession: options.ParentSession})
		if err != nil {
			return nil, err
		}
		if path == "" {
			return nil, nil
		}
		return path, nil
	case "setSessionFile":
		path, err := args.String(0)
		if err != nil {
			return nil, err
		}
		return nil, session.SetSessionFile(path)
	}
	return nil, errors.New("SessionManager has no method " + method)
}

// graphEntry is an entry as the raw JSON record Pi holds.
func graphEntry(entry SessionEntry) (json.RawMessage, error) {
	raws, err := sessionentry.ReadRaws([]SessionEntry{entry})
	if err != nil {
		return nil, err
	}
	return raws[0], nil
}

var _ subprocess.SessionGraphObject = (*GraphSessionManager)(nil)

// graphListProgress is the onProgress function the extension named in argument i (SessionListProgress): it reports (loaded, total, partialSessions?) to the extension as the listing runs. A nil result is no function.
func graphListProgress(ctx context.Context, scope subprocess.SessionGraphScope, args GraphArgs, i int) (SessionListProgress, error) {
	id, err := args.OptString(i)
	if err != nil || id == nil {
		return nil, err
	}
	return func(loaded, total int, partial []SessionInfo) {
		var sessions any
		if partial != nil {
			sessions = graphSessionInfos(partial)
		}
		encoded, err := json.Marshal([]any{loaded, total, sessions})
		if err == nil {
			_, _ = scope.Callback(ctx, *id, encoded)
		}
	}, nil
}
