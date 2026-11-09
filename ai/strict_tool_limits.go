package ai

// Ports packages/ai/src/api/strict-tool-limits.ts

import (
	"container/list"
	"slices"
	"strings"
	"sync"
)

// strictToolLimitErrorMessages are the messages Claude rejects a request with, on the Anthropic API and on Bedrock, when its strict
// tools exceed its limits. Observed limits: 20 strict tools, and 16 optional or union-typed parameters across all strict tools.
// Mirrors upstream STRICT_TOOL_LIMIT_ERROR_MESSAGES.
var strictToolLimitErrorMessages = []string{
	"The compiled grammar is too large",
	"Too many strict tools (",
	"Schemas contains too many parameters with union types (",
}

// isStrictToolLimitErrorMessage reports whether a provider error message says the request's strict tools exceed the provider's limits.
// Mirrors upstream isStrictToolLimitErrorMessage.
func isStrictToolLimitErrorMessage(message string) bool {
	return slices.ContainsFunc(strictToolLimitErrorMessages, func(limitMessage string) bool { return strings.Contains(message, limitMessage) })
}

// maxSessionsOverStrictToolLimits is upstream MAX_SESSIONS_OVER_STRICT_TOOL_LIMITS.
const maxSessionsOverStrictToolLimits = 10_000

// strictToolLimitSessions is upstream sessionsOverStrictToolLimits: the sessions whose strict tools Claude rejected for exceeding its
// limits, oldest first like a JavaScript Set. Their later requests send "prefer" tools non-strict right away: the tool set rarely changes
// between turns, so trying strict again would only repeat the rejection, and switching back to strict later would rewrite the tool
// definitions and miss the prompt cache. Shared by the Anthropic and Bedrock providers, which run the same Claude limits.
var strictToolLimitSessions = struct {
	mu    sync.Mutex
	set   map[string]*list.Element
	order *list.List
}{set: map[string]*list.Element{}, order: list.New()}

// markStrictToolLimitsExceeded records that a session's strict tools exceeded Claude's limits; it does nothing without a session. It keeps
// at most maxSessionsOverStrictToolLimits sessions. Entries are normally freed by CleanupSessionResources when a session ends; the cap bounds
// memory for library users that never call it, and a dropped session just pays one more rejection.
func markStrictToolLimitsExceeded(sessionID string) {
	if sessionID == "" {
		return
	}
	sessions := &strictToolLimitSessions
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	if _, ok := sessions.set[sessionID]; !ok {
		sessions.set[sessionID] = sessions.order.PushBack(sessionID)
	}
	if len(sessions.set) > maxSessionsOverStrictToolLimits {
		oldest := sessions.order.Front()
		sessions.order.Remove(oldest)
		delete(sessions.set, oldest.Value.(string))
	}
}

// hasExceededStrictToolLimits reports whether an earlier request of this session had its strict tools rejected for exceeding Claude's limits.
func hasExceededStrictToolLimits(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	sessions := &strictToolLimitSessions
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	_, ok := sessions.set[sessionID]
	return ok
}

func init() {
	RegisterSessionResourceCleanup(func(sessionID string) {
		sessions := &strictToolLimitSessions
		sessions.mu.Lock()
		defer sessions.mu.Unlock()
		if sessionID == "" {
			clear(sessions.set)
			sessions.order.Init()
			return
		}
		if element, ok := sessions.set[sessionID]; ok {
			sessions.order.Remove(element)
			delete(sessions.set, sessionID)
		}
	})
}
