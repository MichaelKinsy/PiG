package ai

import (
	"slices"
	"strings"
	"sync"
)

// OpenRouterRoutingVariants are the routing variants of OpenRouter that PiG offers for every OpenRouter chat model (D199): `:nitro`
// sorts the model's providers by throughput and `:floor` by price. OpenRouter accepts them on any model id and never lists them in
// /models, so Pi's catalog has none; https://openrouter.ai/docs/guides/routing/model-variants/overview. The built-in catalog does
// not store them: a variant is derived from its base entry when it is looked up (LookupModel, LookupModelExact) or listed
// (ListModels, WithOpenRouterRoutingVariants).
var OpenRouterRoutingVariants = []string{"nitro", "floor"}

// OpenRouterRoutingVariant splits the id of a model of provider into its base id and routing variant. ok is false for any model
// that is not an OpenRouter routing variant.
func OpenRouterRoutingVariant(provider, id string) (base, variant string, ok bool) {
	if provider != "openrouter" { //portlint:allow providerliteral the variants are OpenRouter's own routing suffixes
		return "", "", false
	}
	cut := strings.LastIndexByte(id, ':')
	if cut < 0 || !slices.Contains(OpenRouterRoutingVariants, id[cut+1:]) {
		return "", "", false
	}
	return id[:cut], id[cut+1:], true
}

// OpenRouterVariant is the routing variant of m, an OpenRouter chat entry that is not itself a variant.
//
// pig divergence (D199): Pi lists only what OpenRouter's /models returns. OpenRouter's variants page tells model pickers to derive
// the routing variants from the base entries and to read every other property from the base entry ("Offering routing variants in a
// model picker"), so a variant repeats its base model's api, endpoint, pricing, limits, capabilities and thinking levels and
// differs in its id, its name ("<name> (nitro)") and its JSON record.
func (m *GeneratedModel) OpenRouterVariant(variant string) GeneratedModel {
	derived := *m
	derived.ID = m.ID + ":" + variant
	derived.DisplayName = m.DisplayName + " (" + variant + ")"
	if m.Record != "" {
		derived.Record = rewriteRecordIdentity(m.Record, derived.ID, derived.DisplayName)
	}
	return derived
}

// IsOpenRouterBase reports whether the model id of provider is an OpenRouter model that takes routing variants, one that is not itself a variant.
func IsOpenRouterBase(provider, id string) bool {
	_, _, variant := OpenRouterRoutingVariant(provider, id)
	return provider == "openrouter" && !variant //portlint:allow providerliteral the variants are OpenRouter's own routing suffixes
}

func isOpenRouterBase(m *GeneratedModel) bool { return IsOpenRouterBase(m.Provider, m.ID) }

// bundledOpenRouterIDs is the set of the ids of the bundled OpenRouter models.
var bundledOpenRouterIDs = sync.OnceValue(func() map[string]struct{} {
	ids := map[string]struct{}{}
	for i := range GeneratedModels {
		if GeneratedModels[i].Provider == "openrouter" { //portlint:allow providerliteral the variants are OpenRouter's own routing suffixes
			ids[GeneratedModels[i].ID] = struct{}{}
		}
	}
	return ids
})

// OpenRouterRoutingVariantIDs lists the ids of the routing variants of the bundled OpenRouter model id that the bundled catalog does not
// itself list, in OpenRouter's order, or nil when id is not a bundled OpenRouter model that takes variants. It is the id side of
// WithOpenRouterRoutingVariants for a caller that builds no GeneratedModel.
func OpenRouterRoutingVariantIDs(provider, id string) []string {
	if !IsOpenRouterBase(provider, id) {
		return nil
	}
	var ids []string
	bundled := bundledOpenRouterIDs()
	for _, variant := range OpenRouterRoutingVariants {
		if _, listed := bundled[id+":"+variant]; !listed {
			ids = append(ids, id+":"+variant)
		}
	}
	return ids
}

// WithOpenRouterRoutingVariants lists each routing variant of every OpenRouter chat model of models right after the model, leaving
// a variant the catalog already lists where it is. It returns models itself when it holds no OpenRouter model. A bundled model's variants are
// derived once and held (derivedOpenRouterVariant), so a list costs a copy of each entry listed.
func WithOpenRouterRoutingVariants(models []GeneratedModel) []GeneratedModel {
	bases, hasVariant := 0, false
	for i := range models {
		if isOpenRouterBase(&models[i]) {
			bases++
		} else if _, _, variant := OpenRouterRoutingVariant(models[i].Provider, models[i].ID); variant {
			hasVariant = true
		}
	}
	if bases == 0 {
		return models
	}
	// A variant the list already holds is rare (Pi's catalog lists none); the set of them is built only then.
	var listed map[string]struct{}
	if hasVariant {
		listed = make(map[string]struct{}, len(models))
		for i := range models {
			if models[i].Provider == "openrouter" { //portlint:allow providerliteral the variants are OpenRouter's own routing suffixes
				listed[models[i].ID] = struct{}{}
			}
		}
	}
	out := make([]GeneratedModel, 0, len(models)+bases*len(OpenRouterRoutingVariants))
	for i := range models {
		out = append(out, models[i])
		if !isOpenRouterBase(&models[i]) {
			continue
		}
		for _, variant := range OpenRouterRoutingVariants {
			if _, exists := listed[models[i].ID+":"+variant]; hasVariant && exists {
				continue
			}
			out = append(out, *derivedOpenRouterVariant(&models[i], variant))
		}
	}
	return out
}

// WithOpenRouterRoutingVariantModels is WithOpenRouterRoutingVariants for a list of models a catalog refresh saved, which replaces the bundled
// catalog of OpenRouter: it lists each routing variant of every OpenRouter chat model of models right after the model, leaving a variant the list
// already holds where it is. The variants are derived each time the list is read and never saved, so they follow the current list whether it is the
// bundled catalog or a refreshed one. It returns models itself when it holds no OpenRouter chat model.
//
// pig divergence (D199): see OpenRouterRoutingVariants.
func WithOpenRouterRoutingVariantModels(models []AnyModel) []AnyModel {
	bases, hasVariant := 0, false
	for _, model := range models {
		chat, ok := openRouterChatModel(model)
		if !ok {
			continue
		}
		if _, _, variant := OpenRouterRoutingVariant(chat.ProviderMeta.ProviderID, chat.ID); variant {
			hasVariant = true
		} else {
			bases++
		}
	}
	if bases == 0 {
		return models
	}
	var listed map[string]struct{}
	if hasVariant {
		listed = make(map[string]struct{}, len(models))
		for _, model := range models {
			if chat, ok := openRouterChatModel(model); ok {
				listed[chat.ID] = struct{}{}
			}
		}
	}
	out := make([]AnyModel, 0, len(models)+bases*len(OpenRouterRoutingVariants))
	for _, model := range models {
		out = append(out, model)
		chat, ok := openRouterChatModel(model)
		if !ok || !IsOpenRouterBase(chat.ProviderMeta.ProviderID, chat.ID) {
			continue
		}
		for _, variant := range OpenRouterRoutingVariants {
			if _, exists := listed[chat.ID+":"+variant]; exists {
				continue
			}
			out = append(out, chat.openRouterVariant(variant))
		}
	}
	return out
}

// openRouterChatModel is model as a chat model of OpenRouter, the only models that take routing variants.
func openRouterChatModel(model AnyModel) (*Model, bool) {
	chat, ok := model.(*Model)
	if !ok || chat == nil || chat.ProviderMeta.ProviderID != "openrouter" || !IsModelType(model, ModelTypeChat) { //portlint:allow providerliteral the variants are OpenRouter's own routing suffixes
		return nil, false
	}
	return chat, true
}

// openRouterVariant is the routing variant of m, an OpenRouter chat model that is not itself a variant: a copy of m in everything but its id,
// name and record, as GeneratedModel.OpenRouterVariant derives it from a bundled entry.
func (m *Model) openRouterVariant(variant string) *Model {
	derived := *m
	derived.ID = m.ID + ":" + variant
	derived.DisplayName = m.DisplayName + " (" + variant + ")"
	if m.catalogRecord != "" {
		derived.catalogRecord = rewriteRecordIdentity(m.catalogRecord, derived.ID, derived.DisplayName)
	}
	return &derived
}

// derivedVariantKey names the variant of a bundled OpenRouter model.
type derivedVariantKey struct{ base, variant string }

// derivedVariant is a variant derived from a bundled base entry, with the name and record of the base it was derived from.
type derivedVariant struct {
	baseName, baseRecord string
	entry                *GeneratedModel
}

// derivedVariants holds the variants derived from the bundled OpenRouter models, each built once and never written: a listing copies the
// entry and a lookup returns it. It holds at most one entry per variant of each bundled model, so it is bounded by the catalog.
var derivedVariants sync.Map

// derivedOpenRouterVariant is base.OpenRouterVariant(variant), built once for a base that is a bundled model and derived afresh for any other.
func derivedOpenRouterVariant(base *GeneratedModel, variant string) *GeneratedModel {
	key := derivedVariantKey{base.ID, variant}
	if cached, ok := derivedVariants.Load(key); ok {
		if derived := cached.(*derivedVariant); derived.baseName == base.DisplayName && derived.baseRecord == base.Record {
			return derived.entry
		}
		entry := base.OpenRouterVariant(variant)
		return &entry
	}
	entry := base.OpenRouterVariant(variant)
	actual, _ := derivedVariants.LoadOrStore(key, &derivedVariant{baseName: base.DisplayName, baseRecord: base.Record, entry: &entry})
	if derived := actual.(*derivedVariant); derived.baseName == base.DisplayName && derived.baseRecord == base.Record {
		return derived.entry
	}
	return &entry
}

// openRouterVariants holds the variant entries LookupModel and LookupModelExact have derived, by the spec they were asked for, so a
// repeated lookup returns one entry.
var openRouterVariants sync.Map

// lookupOpenRouterVariant resolves spec, "<base>:<variant>", from the entry base finds for "<base>"; a base that is no OpenRouter
// entry that takes variants is no match. It is called only for a spec the catalog does not list.
func lookupOpenRouterVariant(spec string, base func(string) (*GeneratedModel, bool)) (*GeneratedModel, bool) {
	cut := strings.LastIndexByte(spec, ':')
	if cut < 0 || !slices.Contains(OpenRouterRoutingVariants, spec[cut+1:]) {
		return nil, false
	}
	if cached, ok := openRouterVariants.Load(spec); ok {
		return cached.(*GeneratedModel), true
	}
	entry, ok := base(spec[:cut])
	if !ok || !isOpenRouterBase(entry) {
		return nil, false
	}
	derived := derivedOpenRouterVariant(entry, spec[cut+1:])
	actual, _ := openRouterVariants.LoadOrStore(spec, derived)
	return actual.(*GeneratedModel), true
}

// rewriteRecordIdentity returns record, a compact JSON object, with its top-level "id" and "name" values replaced and every other
// byte kept, so the members keep their order. A record without both members is returned unchanged.
func rewriteRecordIdentity(record, id, name string) string {
	var out strings.Builder
	out.Grow(len(record) + len(id) + len(name))
	copied, replaced := 0, 0
	i := skipSpace(record, 0)
	if i >= len(record) || record[i] != '{' {
		return record
	}
	i++
	for {
		i = skipSpace(record, i)
		if i >= len(record) || record[i] == '}' {
			break
		}
		if record[i] == ',' {
			i++
			continue
		}
		if record[i] != '"' {
			return record
		}
		keyEnd := skipString(record, i)
		key := record[i+1 : keyEnd-1]
		i = skipSpace(record, keyEnd)
		if i >= len(record) || record[i] != ':' {
			return record
		}
		valueStart := skipSpace(record, i+1)
		valueEnd := skipValue(record, valueStart)
		if valueEnd <= valueStart {
			return record
		}
		switch key {
		case "id", "name":
			value := id
			if key == "name" {
				value = name
			}
			out.WriteString(record[copied:valueStart])
			writeJSONString(&out, value)
			copied = valueEnd
			replaced++
		}
		i = valueEnd
	}
	if replaced != 2 {
		return record
	}
	out.WriteString(record[copied:])
	return out.String()
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

// skipString returns the index after the string that starts at s[i].
func skipString(s string, i int) int {
	for i++; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(s)
}

// skipValue returns the index after the JSON value that starts at s[i].
func skipValue(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '"':
		return skipString(s, i)
	case '{', '[':
		for depth := 0; i < len(s); i++ {
			switch s[i] {
			case '"':
				i = skipString(s, i) - 1
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					return i + 1
				}
			}
		}
		return len(s)
	}
	for i < len(s) && s[i] != ',' && s[i] != '}' && s[i] != ' ' && s[i] != '\n' {
		i++
	}
	return i
}

// writeJSONString writes value as JSON.stringify does: quotes, backslashes and control characters escaped, <, > and & left as they are.
func writeJSONString(out *strings.Builder, value string) {
	const hex = "0123456789abcdef"
	out.WriteByte('"')
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case c == '"' || c == '\\':
			out.WriteByte('\\')
			out.WriteByte(c)
		case c == '\n':
			out.WriteString(`\n`)
		case c == '\r':
			out.WriteString(`\r`)
		case c == '\t':
			out.WriteString(`\t`)
		case c == '\b':
			out.WriteString(`\b`)
		case c == '\f':
			out.WriteString(`\f`)
		case c < 0x20:
			out.WriteString(`\u00`)
			out.WriteByte(hex[c>>4])
			out.WriteByte(hex[c&15])
		default:
			out.WriteByte(c)
		}
	}
	out.WriteByte('"')
}
