package sdk

import (
	"errors"
	"os"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// SessionManager is Pi's `SessionManager` class (core/session-manager.ts): an append-only session log an extension constructs, reads and appends to. The host owns the log; a SessionManager built here and passed to CreateAgentSession is the very log the child session appends to. ctx.SessionManager() is Pi's ReadonlySessionManager, the [ReadonlySessionManager] of the running session.
type SessionManager struct{ object *graphObject }

// NewSessionOptions is Pi's NewSessionOptions. A nil member is Pi's undefined.
type NewSessionOptions struct {
	ID            *string `json:"id,omitempty"`
	ParentSession *string `json:"parentSession,omitempty"`
}

// SessionInfo is Pi's SessionInfo, one session file as SessionManagerList reports it.
type SessionInfo struct {
	Path              string    `json:"path"`
	ID                string    `json:"id"`
	Cwd               string    `json:"cwd"`
	Name              *string   `json:"name,omitempty"`
	ParentSessionPath *string   `json:"parentSessionPath,omitempty"`
	Created           time.Time `json:"created"`
	Modified          time.Time `json:"modified"`
	MessageCount      int       `json:"messageCount"`
	FirstMessage      string    `json:"firstMessage"`
	AllMessagesText   string    `json:"allMessagesText"`
}

func sessionManagerOf(c Context, op string, args ...any) (SessionManager, error) {
	object, err := newGraphObject(c, "SessionManager", op, args...)
	return SessionManager{object: object}, err
}

// SessionManagerCreate is `SessionManager.create(cwd, sessionDir?, options?)`: a new persisted session. A nil sessionDir is the default session directory of cwd.
func SessionManagerCreate(c Context, cwd string, sessionDir *string, options *NewSessionOptions) (SessionManager, error) {
	return sessionManagerOf(c, "create", cwd, sessionDir, options)
}

// SessionManagerOpen is `SessionManager.open(path, sessionDir?, cwdOverride?)`: the session file at path. A nil sessionDir is the file's directory and a nil cwdOverride the header's cwd.
func SessionManagerOpen(c Context, path string, sessionDir, cwdOverride *string) (SessionManager, error) {
	return sessionManagerOf(c, "open", path, sessionDir, cwdOverride)
}

// SessionManagerContinueRecent is `SessionManager.continueRecent(cwd, sessionDir?)`: the most recent session of the directory, or a new one when there is none.
func SessionManagerContinueRecent(c Context, cwd string, sessionDir *string) (SessionManager, error) {
	return sessionManagerOf(c, "continueRecent", cwd, sessionDir)
}

// SessionManagerInMemory is `SessionManager.inMemory(cwd = process.cwd(), options?, entries?)`: a session that is never written. An empty cwd is the process's working directory; entries are the records (the header optional) the session starts from.
func SessionManagerInMemory(c Context, cwd string, options *NewSessionOptions, entries []map[string]any) (SessionManager, error) {
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return SessionManager{}, err
		}
	}
	return sessionManagerOf(c, "inMemory", cwd, options, entries)
}

// SessionManagerForkFrom is `SessionManager.forkFrom(sourcePath, targetCwd, sessionDir?, options?)`: a new session in targetCwd with the whole history of the source session.
func SessionManagerForkFrom(c Context, sourcePath, targetCwd string, sessionDir *string, options *NewSessionOptions) (SessionManager, error) {
	return sessionManagerOf(c, "forkFrom", sourcePath, targetCwd, sessionDir, options)
}

// SessionManagerFindByID is `SessionManager.findById(cwd, id, sessionDir?)`: the path of the session file with that exact id, nil when there is none.
func SessionManagerFindByID(c Context, cwd, id string, sessionDir *string) (*string, error) {
	return graphStatic[*string](c, "SessionManager", "findById", cwd, id, sessionDir)
}

// SessionListProgress is Pi's SessionListProgress: loaded of total session files are read. partial holds the sessions read so far, sorted by activity, on the periodic updates that carry them and is nil on the others.
type SessionListProgress func(loaded, total int, partial []SessionInfo)

func (c Context) listProgressID(onProgress SessionListProgress) any {
	if onProgress == nil {
		return nil
	}
	return c.callbackID(func(args []json.RawMessage) (any, error) {
		var loaded, total int
		var partial []SessionInfo
		if len(args) < 3 {
			return nil, errors.New("session list progress: expected loaded, total and partialSessions")
		}
		if err := json.Unmarshal(args[0], &loaded); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(args[1], &total); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(args[2], &partial); err != nil {
			return nil, err
		}
		onProgress(loaded, total, partial)
		return nil, nil
	})
}

// SessionManagerList is `SessionManager.list(cwd, sessionDir?, onProgress?)`: the sessions of a directory, most recently modified first. onProgress, when not nil, is called as session files are read. Cancelling the context's request cancels the listing, as Pi's AbortSignal does.
func SessionManagerList(c Context, cwd string, sessionDir *string, onProgress SessionListProgress) ([]SessionInfo, error) {
	return graphStaticAsync[[]SessionInfo](c, "SessionManager", "list", cwd, sessionDir, c.listProgressID(onProgress))
}

// SessionManagerListAll is `SessionManager.listAll(sessionDir?, onProgress?)`: the sessions of every project (or of sessionDir), most recently modified first.
func SessionManagerListAll(c Context, sessionDir *string, onProgress SessionListProgress) ([]SessionInfo, error) {
	return graphStaticAsync[[]SessionInfo](c, "SessionManager", "listAll", sessionDir, c.listProgressID(onProgress))
}

// Handle is the host's name for the log, which CreateAgentSession puts on the wire.
func (s SessionManager) Handle() string { return s.object.Handle() }

func (s SessionManager) GetCwd() (string, error) { return graphMethodCall[string](s.object, "getCwd") }
func (s SessionManager) GetSessionDir() (string, error) {
	return graphMethodCall[string](s.object, "getSessionDir")
}
func (s SessionManager) GetSessionID() (string, error) {
	return graphMethodCall[string](s.object, "getSessionId")
}
func (s SessionManager) GetSessionFile() (*string, error) {
	return graphMethodCall[*string](s.object, "getSessionFile")
}
func (s SessionManager) GetSessionName() (*string, error) {
	return graphMethodCall[*string](s.object, "getSessionName")
}
func (s SessionManager) GetLeafID() (*string, error) {
	return graphMethodCall[*string](s.object, "getLeafId")
}
func (s SessionManager) GetHeader() (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "getHeader")
}
func (s SessionManager) GetLeafEntry() (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "getLeafEntry")
}
func (s SessionManager) GetEntry(id string) (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "getEntry", id)
}
func (s SessionManager) GetLabel(id string) (*string, error) {
	return graphMethodCall[*string](s.object, "getLabel", id)
}
func (s SessionManager) GetEntryCount() (int, error) {
	return graphMethodCall[int](s.object, "getEntryCount")
}
func (s SessionManager) GetEntries() ([]map[string]any, error) {
	return graphMethodCall[[]map[string]any](s.object, "getEntries")
}
func (s SessionManager) GetBranch(fromID *string) ([]map[string]any, error) {
	return graphMethodCall[[]map[string]any](s.object, "getBranch", fromID)
}
func (s SessionManager) GetChildren(parentID string) ([]map[string]any, error) {
	return graphMethodCall[[]map[string]any](s.object, "getChildren", parentID)
}
func (s SessionManager) GetTree() ([]SessionTreeNode, error) {
	return graphMethodCall[[]SessionTreeNode](s.object, "getTree")
}
func (s SessionManager) BuildContextEntries() ([]map[string]any, error) {
	return graphMethodCall[[]map[string]any](s.object, "buildContextEntries")
}
func (s SessionManager) BuildSessionProjection() (SessionProjection, error) {
	return graphMethodCall[SessionProjection](s.object, "buildSessionProjection")
}
func (s SessionManager) BuildSessionContext() (SessionContext, error) {
	return graphMethodCall[SessionContext](s.object, "buildSessionContext")
}
func (s SessionManager) IsPersisted() (bool, error) {
	return graphMethodCall[bool](s.object, "isPersisted")
}
func (s SessionManager) UsesDefaultSessionDir() (bool, error) {
	return graphMethodCall[bool](s.object, "usesDefaultSessionDir")
}

// AppendMessage appends a message (a Pi Message, CustomMessage or BashExecutionMessage as a JSON object) and returns the entry id.
func (s SessionManager) AppendMessage(message map[string]any) (string, error) {
	return graphMethodCall[string](s.object, "appendMessage", message)
}

// AppendThinkingLevelChange records a thinking level change.
func (s SessionManager) AppendThinkingLevelChange(thinkingLevel string) (string, error) {
	return graphMethodCall[string](s.object, "appendThinkingLevelChange", thinkingLevel)
}

// AppendModelChange records a model change.
func (s SessionManager) AppendModelChange(provider, modelID string) (string, error) {
	return graphMethodCall[string](s.object, "appendModelChange", provider, modelID)
}

// AppendUsage records the usage of work outside the conversation and returns the entry. A nil note is Pi's undefined.
func (s SessionManager) AppendUsage(kind, provider, model string, usage map[string]any, note *string) (map[string]any, error) {
	return graphMethodCall[map[string]any](s.object, "appendUsage", kind, provider, model, usage, note)
}

// AppendCompaction records a compaction. details, fromHook and usage are Pi's optional parameters (nil is undefined).
func (s SessionManager) AppendCompaction(summary, firstKeptEntryID string, tokensBefore int, details any, fromHook *bool, usage map[string]any) (string, error) {
	return graphMethodCall[string](s.object, "appendCompaction", summary, firstKeptEntryID, tokensBefore, details, fromHook, usage)
}

// AppendCustomEntry appends an extension entry that does not take part in the model context.
func (s SessionManager) AppendCustomEntry(customType string, data any) (string, error) {
	return graphMethodCall[string](s.object, "appendCustomEntry", customType, data)
}

// AppendSessionInfo sets the session name.
func (s SessionManager) AppendSessionInfo(name string) (string, error) {
	return graphMethodCall[string](s.object, "appendSessionInfo", name)
}

// AppendCustomMessageEntry appends an extension message that takes part in the model context.
func (s SessionManager) AppendCustomMessageEntry(customType string, content any, display bool, details any) (string, error) {
	return graphMethodCall[string](s.object, "appendCustomMessageEntry", customType, content, display, details)
}

// AppendContextEdit records a replacement of the content of an earlier entry in the model context.
func (s SessionManager) AppendContextEdit(targetID string, replacement map[string]any) (string, error) {
	return graphMethodCall[string](s.object, "appendContextEdit", targetID, replacement)
}

// AppendLabelChange sets or clears the label of an entry; a nil label clears it.
func (s SessionManager) AppendLabelChange(targetID string, label *string) (string, error) {
	return graphMethodCall[string](s.object, "appendLabelChange", targetID, label)
}

// Branch moves the leaf to an earlier entry.
func (s SessionManager) Branch(branchFromID string) error {
	_, err := graphMethodCall[graphDone](s.object, "branch", branchFromID)
	return err
}

// ResetLeaf moves the leaf before the first entry, so the next append is a root.
func (s SessionManager) ResetLeaf() error {
	_, err := graphMethodCall[graphDone](s.object, "resetLeaf")
	return err
}

// BranchWithSummary moves the leaf to branchFromID (nil is the root) and appends a branch summary; it returns the entry id.
func (s SessionManager) BranchWithSummary(branchFromID *string, summary string, details any, fromHook *bool, usage map[string]any) (string, error) {
	return graphMethodCall[string](s.object, "branchWithSummary", branchFromID, summary, details, fromHook, usage)
}

// CreateBranchedSession starts a new session holding the path to leafID. It returns the new session file, nil for a session that is not persisted.
func (s SessionManager) CreateBranchedSession(leafID string) (*string, error) {
	return graphMethodCall[*string](s.object, "createBranchedSession", leafID)
}

// NewSession resets the manager to an empty session. It returns the new session file, nil for a session that is not persisted.
func (s SessionManager) NewSession(options *NewSessionOptions) (*string, error) {
	return graphMethodCall[*string](s.object, "newSession", options)
}

// SetSessionFile switches the manager to another session file.
func (s SessionManager) SetSessionFile(sessionFile string) error {
	_, err := graphMethodCall[graphDone](s.object, "setSessionFile", sessionFile)
	return err
}
