package data

import (
	"context"
	"math"
	"runtime"
	"slices"
	"strings"
	"sync"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next/data/principal.ts: the app's caller identities, and the keys Chord derives from them.

// Principal is an app's principal: plain JSON with a `kind`. Chord never persists it. It is a pointer so that a
// principal has the identity upstream's per-object cache is keyed by.
type Principal = *delta.JsonObject

// PrincipalKeyFunction is an app's own principal key function; it must be the same function on every host.
//
// Go forced difference: a Go func value has no identity to key a cache by (two closures of one literal compare
// equal by code pointer), so the function is held in a struct whose pointer is its identity and which owns the cache
// of the keys it computed. A live principal therefore never retains a dropped function, as upstream's
// WeakMap-of-WeakMaps guarantees, because the function's cache lives in the function.
type PrincipalKeyFunction struct {
	key  func(Principal) string
	keys principalKeys
}

// NewPrincipalKeyFunction wraps an app's key function; the result is the function's identity.
func NewPrincipalKeyFunction(key func(Principal) string) *PrincipalKeyFunction {
	return &PrincipalKeyFunction{key: key}
}

// PrincipalKey is the key of a principal: its canonical JSON, or customKey's result. Cached per principal object; a
// custom key's cache is owned by the function too, so a live principal never retains a dropped function. customKey nil
// is upstream's absent argument.
func PrincipalKey(principal Principal, ctx context.Context, customKey *PrincipalKeyFunction) string {
	cache := &defaultKeys
	if customKey != nil {
		cache = &customKey.keys
	}
	if key, ok := cache.get(principal); ok {
		return key
	}
	var key string
	if customKey == nil {
		key = CanonicalJson(principal, ctx)
	} else {
		key = customKey.key(principal)
	}
	cache.set(principal, key)
	return key
}

// defaultKeys is the cache of default keys, by principal.
var defaultKeys principalKeys

// principalKeys is a cache of principal keys that does not retain its principals: entries are keyed by weak pointer
// and removed once the principal is collected.
type principalKeys struct {
	mu      sync.Mutex
	entries map[weak.Pointer[delta.JsonObject]]string
}

func (cache *principalKeys) get(principal Principal) (string, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	key, ok := cache.entries[weak.Make(principal)]
	return key, ok
}

func (cache *principalKeys) set(principal Principal, key string) {
	ref := weak.Make(principal)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, ok := cache.entries[ref]; ok {
		cache.entries[ref] = key
		return
	}
	if cache.entries == nil {
		cache.entries = map[weak.Pointer[delta.JsonObject]]string{}
	}
	cache.entries[ref] = key
	// The cleanup holds the cache weakly: a custom function's cache must be collectable while the principal lives.
	owner := weak.Make(cache)
	runtime.AddCleanup(principal, func(ref weak.Pointer[delta.JsonObject]) {
		if live := owner.Value(); live != nil {
			live.mu.Lock()
			delete(live.entries, ref)
			live.mu.Unlock()
		}
	}, ref)
}

// CanonicalJson is canonical JSON: sorted object keys (by UTF-16 code unit, as JavaScript's sort), `-0` written as `0`.
// Counts one walk.
func CanonicalJson(value delta.JsonValue, ctx context.Context) string {
	chordctx.Telemetry(ctx).Metrics().Counter("chord.data.walks", telemetry.InstrumentOptions{}).Add(1, nil)
	var out strings.Builder
	writeCanonical(&out, value)
	return out.String()
}

func writeCanonical(out *strings.Builder, value delta.JsonValue) {
	switch typed := value.(type) {
	case []any:
		out.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				out.WriteByte(',')
			}
			writeCanonical(out, item)
		}
		out.WriteByte(']')
	case *delta.JsonObject:
		writeCanonicalObject(out, typed.Keys(), func(key string) delta.JsonValue { return typed.Value(key) })
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		writeCanonicalObject(out, keys, func(key string) delta.JsonValue { return typed[key] })
	case float64:
		// JSON.stringify writes -0 as 0.
		if typed == 0 {
			typed = math.Abs(typed)
		}
		writeScalar(out, typed)
	default:
		writeScalar(out, value)
	}
}

func writeCanonicalObject(out *strings.Builder, keys []string, member func(string) delta.JsonValue) {
	keys = slices.Clone(keys)
	slices.SortFunc(keys, compareUTF16)
	out.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			out.WriteByte(',')
		}
		writeScalar(out, key)
		out.WriteByte(':')
		writeCanonical(out, member(key))
	}
	out.WriteByte('}')
}

// writeScalar writes JSON.stringify(value) of a string, number, boolean or null.
func writeScalar(out *strings.Builder, value any) {
	text, err := jsstring.MarshalJSON(value)
	if err != nil {
		// Only a non-JSON value fails; canonical JSON of strict JSON never reaches here.
		panic("data: canonical JSON of a non-JSON value: " + err.Error())
	}
	out.Write(text)
}

// compareUTF16 orders strings by UTF-16 code unit, as JavaScript's default sort does.
func compareUTF16(left, right string) int {
	leftUnits, rightUnits := jsstring.ToUTF16(left), jsstring.ToUTF16(right)
	return slices.Compare(leftUnits, rightUnits)
}
