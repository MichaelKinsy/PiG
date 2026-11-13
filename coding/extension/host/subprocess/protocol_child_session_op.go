package subprocess

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// CallChildSessionOp runs one member of Pi's AgentSession on a session, for the members that have no call of their own above. Args is a ChildSessionOp and the result is the member's value in Pi's JSON shape (ChildSessionOp.Op lists them). It is for the Go, Python and Rust SDKs; Node runs Pi's own AgentSession.
//
// Calls on one session run in the order they were sent.
const CallChildSessionOp = "childSession.op"

// CallChildSessionOpAsync is CallChildSessionOp for a member Pi declares `async` (waitForIdle, compact, navigateTree, reload, executeBash, exportToHtml, summarizeForBugReport, setModel, cycleModel, sendCustomMessage, sendUserMessage): every `async` member of agent-session.ts that has no call of its own (prompt, steer, followUp, abort and bindExtensions have). It starts in order and returns when the Promise settles; the call's lane advances first, so an abort sent next never waits for it.
const CallChildSessionOpAsync = "childSession.opAsync"

// ChildSessionOp is the CallChildSessionOp argument.
//
// Op is spelled as Pi's AgentSession member; the argument object and the result of each are:
//
//	bool:        isStreaming isIdle isCompacting isRetrying isBashRunning hasPendingBashMessages autoCompactionEnabled autoRetryEnabled supportsThinking
//	number:      retryAttempt pendingMessageCount
//	string:      sessionId systemPrompt thinkingLevel steeringMode followUpMode
//	string|null: sessionFile sessionName getLastAssistantText
//	model|null:  model (a Pi Model object); routedModel: {model, thinkingLevel?}|null; scopedModels: [{model, thinkingLevel?}]
//	[string]:    getActiveToolNames getCallableToolNames getSteeringMessages getFollowUpMessages getAvailableThinkingLevels
//	getAllTools: [ToolInfo]; getToolDefinition {name}: ToolInfo|null, the definition's data (its execute and hooks are functions of the extension that registered the tool and do not cross the wire)
//	getContextUsage: {tokens, contextWindow, percent}|null; getSessionStats: SessionStats; getUserMessagesForForking: [{entryId, text}]; promptTemplates: [PromptTemplate]; cacheWarmingStatus: CacheWarmingStatus|null
//	hasExtensionHandlers {eventType}: bool
//	setModel {model, persist?}; cycleModel {direction?: "forward"|"backward", persist?}: {model, thinkingLevel, isScoped}|null; setThinkingLevel {level, persist?}; cycleThinkingLevel {persist?}: string|null
//	setScopedModels {models: [{model, thinkingLevel?}]}; setActiveToolsByName {names}; setSteeringMode {mode}; setFollowUpMode {mode}; setAutoCompactionEnabled {enabled}; setAutoRetryEnabled {enabled}; setCacheWarmingMode {mode}; setSessionName {name}
//	clearQueue: {steering: [string], followUp: [string]}
//	abortBash abortBranchSummary abortCompaction abortRetry waitForIdle reload: no result
//	compact {customInstructions?}: {summary, firstKeptEntryId, tokensBefore, estimatedTokensAfter?, usage?, details?}
//	navigateTree {targetId, summarize?, customInstructions?, replaceInstructions?, label?}: {cancelled, aborted?, editorText?, summaryEntry?}
//	executeBash {command, excludeFromContext?}: {output, exitCode?, cancelled, truncated, fullOutputPath?}; recordBashResult {command, result, excludeFromContext?}
//	exportToHtml {outputPath?, themeName?}: string; exportToJsonl {outputPath?}: string; summarizeForBugReport {hint?}: string
//	sendCustomMessage {message: {customType, content, display, details?}, options?: {triggerTurn?, deliverAs?}}; sendUserMessage {content, options?: {deliverAs?, expandPromptTemplates?}}
//
// A member Pi types `X | undefined` answers null for undefined. An unknown Op fails the call with ChildSessionUnknownOpMessage.
type ChildSessionOp struct {
	ID string `json:"id"`
	Op string `json:"op"`
	// Args is the member's argument object, absent for a member that takes none.
	Args json.RawMessage `json:"args,omitempty"`
}

// ChildSessionUnknownOpMessage is the error of a CallChildSessionOp whose Op the host does not serve; the member's name follows it.
const ChildSessionUnknownOpMessage = "unknown child session member: "
