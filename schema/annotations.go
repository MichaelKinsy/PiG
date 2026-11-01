package schema

// Ports packages/schema/src/annotations.ts
//
// Annotation tracking for `unevaluatedProperties` and `unevaluatedItems`: annotating checks record which properties and items a schema evaluated. Only schemas that use those keywords compile into these closures.

// evaluated is what a schema evaluated: property names (or all of them), the evaluated item prefix, items matched by `contains`.
type evaluated struct {
	props    map[string]struct{}
	allProps bool
	items    int
	allItems bool
	indexes  map[int]struct{}
}

// annotatingCheck is a check that records what it evaluated into a when it passes.
type annotatingCheck func(value any, a *evaluated) bool

func merge(a, b *evaluated) {
	for k := range b.props {
		addProp(a, k)
	}
	a.allProps = a.allProps || b.allProps
	if b.items > a.items {
		a.items = b.items
	}
	a.allItems = a.allItems || b.allItems
	for i := range b.indexes {
		addIndex(a, i)
	}
}

func addProp(a *evaluated, key string) {
	if a.props == nil {
		a.props = map[string]struct{}{}
	}
	a.props[key] = struct{}{}
}

func addIndex(a *evaluated, index int) {
	if a.indexes == nil {
		a.indexes = map[int]struct{}{}
	}
	a.indexes[index] = struct{}{}
}

func annotatingTrue(any, *evaluated) bool  { return true }
func annotatingFalse(any, *evaluated) bool { return false }

// annotatingSlot holds an annotating check that is set later.
type annotatingSlot struct{ check annotatingCheck }

// indirectAnnotating is the indirection a recursive reference goes through.
func indirectAnnotating(slot *annotatingSlot) annotatingCheck {
	return func(v any, a *evaluated) bool { return slot.check(v, a) }
}

// ignoreAnnotations is a plain check in annotating position: it evaluates nothing that unevaluated* sees.
func ignoreAnnotations(check Check) annotatingCheck {
	return func(v any, _ *evaluated) bool { return check(v) }
}

// withFreshAnnotations is an annotating check run as a plain check, with fresh annotations.
func withFreshAnnotations(check annotatingCheck) Check {
	return func(v any) bool { return check(v, &evaluated{}) }
}

// scopedAnnotating runs check with uri pushed onto the dynamic scope.
func scopedAnnotating(scope *scopeStack, uri string, check annotatingCheck) annotatingCheck {
	return func(v any, a *evaluated) bool {
		scope.uris = append(scope.uris, uri)
		ok := check(v, a)
		scope.uris = scope.uris[:len(scope.uris)-1]
		return ok
	}
}

// isolateAnnotations makes unevaluated* see only the annotations of its own schema, never those of its siblings in the parent.
func isolateAnnotations(check annotatingCheck) annotatingCheck {
	return func(v any, a *evaluated) bool {
		b := &evaluated{}
		if !check(v, b) {
			return false
		}
		merge(a, b)
		return true
	}
}

// annotateAll runs the non-annotating keywords, then each annotating part in order.
func annotateAll(plain Check, parts []annotatingCheck) annotatingCheck {
	return func(v any, a *evaluated) bool {
		if !plain(v) {
			return false
		}
		for _, part := range parts {
			if !part(v, a) {
				return false
			}
		}
		return true
	}
}

// namedCheck is a `properties` entry.
type namedCheck struct {
	key   string
	check Check
}

// annotateObject is `properties`, `patternProperties` and `additionalProperties`.
func annotateObject(props []namedCheck, patterns []patternCheck, additional Check) annotatingCheck {
	declared := make(map[string]struct{}, len(props))
	for _, p := range props {
		declared[p.key] = struct{}{}
	}
	return func(v any, a *evaluated) bool {
		o, ok := asObject(v)
		if !ok {
			return true
		}
		var seen []string
		for _, p := range props {
			x, present := o.get(p.key)
			if !present {
				continue
			}
			if !p.check(x) {
				return false
			}
			seen = append(seen, p.key)
		}
		for _, k := range o.keys() {
			_, matched := declared[k]
			x := o.value(k)
			for _, pattern := range patterns {
				if pattern.re.MatchString(k) {
					if !pattern.check(x) {
						return false
					}
					matched = true
					seen = append(seen, k)
				}
			}
			if !matched && additional != nil {
				if !additional(x) {
					return false
				}
				seen = append(seen, k)
			}
		}
		for _, k := range seen {
			addProp(a, k)
		}
		return true
	}
}

// annotateArray is `prefixItems`/`items`/`additionalItems` and `contains` with its bounds.
func annotateArray(prefix []Check, rest, contains Check, minContains, maxContains float64) annotatingCheck {
	return func(v any, a *evaluated) bool {
		list, ok := v.([]any)
		if !ok {
			return true
		}
		m := min(len(list), len(prefix))
		for i := range m {
			if !prefix[i](list[i]) {
				return false
			}
		}
		if rest != nil {
			for i := len(prefix); i < len(list); i++ {
				if !rest(list[i]) {
					return false
				}
			}
			a.allItems = true
		}
		if m > a.items {
			a.items = m
		}
		if contains != nil {
			n := 0.0
			for i, item := range list {
				if contains(item) {
					n++
					addIndex(a, i)
				}
			}
			if n < minContains || n > maxContains {
				return false
			}
		}
		return true
	}
}

// annotateAny is `anyOf` (or `oneOf` when exactlyOne): every passing branch contributes its annotations.
func annotateAny(branches []annotatingCheck, exactlyOne bool) annotatingCheck {
	return func(v any, a *evaluated) bool {
		n := 0
		for _, branch := range branches {
			b := &evaluated{}
			if branch(v, b) {
				n++
				merge(a, b)
			}
		}
		if exactlyOne {
			return n == 1
		}
		return n > 0
	}
}

// annotateIf is `if`, `then` and `else`: a passing `if` contributes its annotations even without `then`.
func annotateIf(cond, then, otherwise annotatingCheck) annotatingCheck {
	return func(v any, a *evaluated) bool {
		b := &evaluated{}
		if cond(v, b) {
			merge(a, b)
			return then == nil || then(v, a)
		}
		return otherwise == nil || otherwise(v, a)
	}
}

// annotateDependent is one `dependentSchemas` entry.
func annotateDependent(key string, check annotatingCheck) annotatingCheck {
	return func(v any, a *evaluated) bool {
		o, ok := asObject(v)
		return !ok || !o.has(key) || check(v, a)
	}
}

// unevaluatedProperties requires every property no other keyword evaluated to pass check.
func unevaluatedProperties(check Check) annotatingCheck {
	return func(v any, a *evaluated) bool {
		o, ok := asObject(v)
		if !ok || a.allProps {
			return true
		}
		for _, k := range o.keys() {
			if _, seen := a.props[k]; !seen && !check(o.value(k)) {
				return false
			}
		}
		a.allProps = true
		return true
	}
}

// unevaluatedItems requires every item no other keyword evaluated to pass check.
func unevaluatedItems(check Check) annotatingCheck {
	return func(v any, a *evaluated) bool {
		list, ok := v.([]any)
		if !ok || a.allItems {
			return true
		}
		for i := a.items; i < len(list); i++ {
			if _, seen := a.indexes[i]; !seen && !check(list[i]) {
				return false
			}
		}
		a.allItems = true
		return true
	}
}
