package pico3

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"sync"
)

var coreKindNames = map[string]bool{"pi.generation": true, "pi.tool": true, "pi.post_tools": true, "pi.collapse": true}

// IsCoreKind reports whether name is one of the fixed core turn kinds.
func IsCoreKind(name string) bool { return coreKindNames[name] }

var thinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

var coreConfigValidators = map[string]func(JsonValue) bool{
	"model": func(value JsonValue) bool {
		object, ok := exactObject(value, []string{"provider", "modelId"}, nil)
		return ok && object["provider"] != "" && object["modelId"] != ""
	},
	"thinkingLevel": func(value JsonValue) bool {
		text, ok := value.(string)
		return ok && slices.Contains(thinkingLevels, text)
	},
	"selectedTools": func(value JsonValue) bool {
		items, ok := value.([]any)
		if !ok {
			return false
		}
		for _, item := range items {
			if _, isText := item.(string); !isText {
				return false
			}
		}
		return true
	},
	"profile": func(value JsonValue) bool {
		_, ok := value.(string)
		return ok
	},
	"retry":        validRetryConfig,
	"threshold":    finiteNumber,
	"keepRecent":   finiteNonnegative,
	"steeringMode": queueMode,
	"followUpMode": queueMode,
}

func validRetryConfig(value JsonValue) bool {
	object, ok := exactObject(value, []string{"enabled", "maxRetries", "baseDelayMs"}, []string{"maxAgentDelayMs"})
	if !ok {
		return false
	}
	if _, isBool := object["enabled"].(bool); !isBool {
		return false
	}
	retries, isNumber := object["maxRetries"].(float64)
	if !isNumber || retries < 0 || retries != float64(int64(retries)) || retries > 1<<53-1 {
		return false
	}
	if !finiteNonnegative(object["baseDelayMs"]) {
		return false
	}
	delay, present := object["maxAgentDelayMs"]
	return !present || finiteNonnegative(delay)
}

func finiteNumber(value JsonValue) bool {
	number, ok := value.(float64)
	return ok && !math.IsNaN(number) && !math.IsInf(number, 0)
}

func finiteNonnegative(value JsonValue) bool {
	number, ok := value.(float64)
	return ok && finiteNumber(number) && number >= 0
}

func queueMode(value JsonValue) bool { return value == "all" || value == "one-at-a-time" }

func exactObject(value JsonValue, required, optional []string) (JsonObject, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	for _, key := range required {
		if _, present := object[key]; !present {
			return nil, false
		}
	}
	for key := range object {
		if !slices.Contains(required, key) && !slices.Contains(optional, key) {
			return nil, false
		}
	}
	return object, true
}

// Defaults holds the declared config defaults derived from registered kinds.
type Defaults struct {
	mu         sync.RWMutex
	rewindable JsonObject
	sticky     JsonObject
	route      map[string]string
	routeOrder []string
	owners     map[string]*Kind
}

func newDefaults() *Defaults {
	return &Defaults{rewindable: JsonObject{}, sticky: JsonObject{}, route: map[string]string{}, owners: map[string]*Kind{}}
}

func (defaults *Defaults) register(kind *Kind) error {
	defaults.mu.Lock()
	defer defaults.mu.Unlock()
	local := map[string]bool{}
	for _, doc := range []string{DocRewindable, DocSticky} {
		for _, declaration := range kind.Config.forDoc(doc) {
			if local[declaration.Key] || defaults.route[declaration.Key] != "" {
				return fmt.Errorf(`config key "%s" declared by more than one kind`, declaration.Key)
			}
			local[declaration.Key] = true
		}
	}
	for _, doc := range []string{DocRewindable, DocSticky} {
		for _, declaration := range kind.Config.forDoc(doc) {
			defaults.route[declaration.Key] = doc
			defaults.routeOrder = append(defaults.routeOrder, declaration.Key)
			defaults.owners[declaration.Key] = kind
			if !declaration.Optional {
				defaults.docLocked(doc)[declaration.Key] = mustStored(declaration.Default)
			}
		}
	}
	return nil
}

func (defaults *Defaults) unregister(kind *Kind) {
	defaults.mu.Lock()
	defer defaults.mu.Unlock()
	for key, owner := range defaults.owners {
		if owner != kind {
			continue
		}
		delete(defaults.owners, key)
		doc := defaults.route[key]
		delete(defaults.route, key)
		defaults.routeOrder = slices.DeleteFunc(defaults.routeOrder, func(candidate string) bool { return candidate == key })
		if doc != "" {
			delete(defaults.docLocked(doc), key)
		}
	}
}

func (defaults *Defaults) docLocked(doc string) JsonObject {
	if doc == DocRewindable {
		return defaults.rewindable
	}
	return defaults.sticky
}

// routeOf returns the document a key lives in; "" when unknown.
func (defaults *Defaults) routeOf(key string) string {
	defaults.mu.RLock()
	defer defaults.mu.RUnlock()
	return defaults.route[key]
}

// keys returns the routed keys in declaration order.
func (defaults *Defaults) keys() []string {
	defaults.mu.RLock()
	defer defaults.mu.RUnlock()
	return slices.Clone(defaults.routeOrder)
}

// defaultOf returns a key's declared default.
func (defaults *Defaults) defaultOf(doc, key string) (JsonValue, bool) {
	defaults.mu.RLock()
	defer defaults.mu.RUnlock()
	value, ok := defaults.docLocked(doc)[key]
	return cloneJSON(value), ok
}

func (defaults *Defaults) validate(key string, value JsonValue) bool {
	if _, err := json.Marshal(value); err != nil {
		return false
	}
	if validator, ok := coreConfigValidators[key]; ok {
		return validator(normalizeNumbers(value))
	}
	return defaults.routeOf(key) != ""
}

func (defaults *Defaults) validateSeed(doc string, seed JsonObject) (JsonObject, error) {
	out := JsonObject{}
	for _, key := range sortedKeys(seed) {
		value := seed[key]
		if defaults.routeOf(key) != doc || !defaults.validate(key, value) {
			return nil, &TypeError{Message: fmt.Sprintf(`invalid %s config value for "%s"`, doc, key)}
		}
		out[key] = mustStored(value)
	}
	return out, nil
}

// fill adds declared defaults for absent keys; a stored null is a value.
func (defaults *Defaults) fill(doc string, target JsonObject) {
	defaults.mu.RLock()
	defer defaults.mu.RUnlock()
	for key, value := range defaults.docLocked(doc) {
		if _, present := target[key]; !present {
			target[key] = cloneJSON(value)
		}
	}
}

func (defaults *Defaults) freshRewindable(over JsonObject, preservePlugins bool) (JsonObject, error) {
	base := JsonObject{"plugins": JsonObject{}}
	defaults.fill(DocRewindable, base)
	raw := JsonObject{}
	for key, value := range over {
		if key != "plugins" {
			raw[key] = value
		}
	}
	rest := raw
	if !preservePlugins {
		validated, err := defaults.validateSeed(DocRewindable, raw)
		if err != nil {
			return nil, err
		}
		rest = validated
	}
	for key, value := range rest {
		base[key] = cloneJSON(value)
	}
	if plugins, ok := over["plugins"]; preservePlugins && ok {
		base["plugins"] = cloneJSON(plugins)
	}
	return base, nil
}

func (defaults *Defaults) freshSticky(over JsonObject) (JsonObject, error) {
	base := JsonObject{"inbox": []any{}, "turn": JsonObject{"tools": []any{}}, "tasks": JsonObject{}, "plugins": JsonObject{}}
	defaults.fill(DocSticky, base)
	raw := JsonObject{}
	for key, value := range over {
		switch key {
		case "inbox", "turn", "tasks", "plugins":
		default:
			raw[key] = value
		}
	}
	validated, err := defaults.validateSeed(DocSticky, raw)
	if err != nil {
		return nil, err
	}
	maps.Copy(base, validated)
	return base, nil
}

// docs caches one tracker per loaded document.
type docs struct {
	mu        sync.Mutex
	trackers  map[string]*Tracker
	sinceBase map[string]int
	storage   Storage
	defaults  *Defaults
}

func newDocs(storage Storage, defaults *Defaults) *docs {
	return &docs{trackers: map[string]*Tracker{}, sinceBase: map[string]int{}, storage: storage, defaults: defaults}
}

func (cache *docs) noteOps(ref DocRef, ops []Op) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if IsBase(ops) {
		cache.sinceBase[ref.key()] = 0
		return
	}
	encoded, _ := json.Marshal(ops)
	cache.sinceBase[ref.key()] += utf16Len(string(encoded))
}

func (cache *docs) opsSinceBase(ref DocRef) int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.sinceBase[ref.key()]
}

func (cache *docs) requestBase(ref DocRef) {
	if tracker := cache.peek(ref); tracker != nil {
		tracker.Rebase()
	}
}

func (cache *docs) get(ctx context.Context, ref DocRef) (*Tracker, error) {
	if tracker := cache.peek(ref); tracker != nil {
		return tracker, nil
	}
	stored, err := cache.storage.Doc(ctx, ref)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, fmt.Errorf("document %s does not exist", ref.key())
	}
	if ref.Doc != DocSession {
		cache.defaults.fill(ref.Doc, stored)
	}
	tracker := Track(stored)
	tracker.Flush()
	cache.adopt(ref, tracker)
	return tracker, nil
}

func (cache *docs) evict(ref DocRef) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	delete(cache.trackers, ref.key())
}

func (cache *docs) peek(ref DocRef) *Tracker {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.trackers[ref.key()]
}

// loaded returns the live state of a loaded document, or nil.
func (cache *docs) loaded(ref DocRef) JsonObject {
	if tracker := cache.peek(ref); tracker != nil {
		return tracker.State()
	}
	return nil
}

func (cache *docs) adopt(ref DocRef, tracker *Tracker) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.trackers[ref.key()] = tracker
}
