package ai

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	id, name := OpenRouterVariantIdentity(m.ID, m.DisplayName, variant)
	return m.openRouterVariantNamed(id, name)
}

// openRouterVariantNamed is the routing variant of m whose id and name, OpenRouterVariantIdentity's for m, the caller holds.
func (m *GeneratedModel) openRouterVariantNamed(id, name string) GeneratedModel {
	derived := *m
	derived.ID, derived.DisplayName = id, name
	if m.Record != "" {
		derived.Record = deriveCatalogRecord(m.Record, id, name)
	}
	return derived
}

// OpenRouterVariantIdentity is the id and the name of the routing variant of the model with id baseID and name baseName. It is the one rule
// that names a variant: every entry, model and listing of a variant takes its id and name from it.
func OpenRouterVariantIdentity(baseID, baseName, variant string) (id, name string) {
	return openRouterVariantID(baseID, variant), baseName + " (" + variant + ")"
}

func openRouterVariantID(baseID, variant string) string { return baseID + ":" + variant }

// IsOpenRouterBase reports whether the model id of provider is an OpenRouter model that takes routing variants, one that is not itself a variant.
func IsOpenRouterBase(provider, id string) bool {
	_, _, variant := OpenRouterRoutingVariant(provider, id)
	return provider == "openrouter" && !variant //portlint:allow providerliteral the variants are OpenRouter's own routing suffixes
}

func isOpenRouterBase(m *GeneratedModel) bool { return IsOpenRouterBase(m.Provider, m.ID) }

// WithOpenRouterRoutingVariants lists each routing variant of every OpenRouter chat model of models right after the model, leaving
// a variant the catalog already lists where it is. It returns models itself when it holds no OpenRouter model. A bundled model's variants are
// derived once and held (openRouterBundle), so a list costs a copy of each entry listed.
func WithOpenRouterRoutingVariants(models []GeneratedModel) []GeneratedModel {
	var bundle *openRouterBundle
	return listOpenRouterRoutingVariants(models, func(base *GeneratedModel, variant string) GeneratedModel {
		if bundle == nil {
			bundle = heldOpenRouterBundle()
		}
		if held := bundle.variant(base, variant); held != nil {
			return *held
		}
		return base.OpenRouterVariant(variant)
	})
}

// SharedOpenRouterRoutingVariants is WithOpenRouterRoutingVariants(models) without the copy when models is the bundled catalog of OpenRouter
// itself (bundledOpenRouterModels): it returns the held list, whose entries are the ones LookupModel and LookupModelExact return, so the caller
// reads it and never writes an element. Any other list is listed as WithOpenRouterRoutingVariants lists it.
func SharedOpenRouterRoutingVariants(models []GeneratedModel) []GeneratedModel {
	if len(models) > 0 {
		if bundle := heldOpenRouterBundle(); len(models) == len(bundle.source) && &models[0] == &bundle.source[0] {
			return bundle.models
		}
	}
	return WithOpenRouterRoutingVariants(models)
}

// openRouterBases counts the models of models that take routing variants.
func openRouterBases(models []GeneratedModel) int {
	bases := 0
	for i := range models {
		if isOpenRouterBase(&models[i]) {
			bases++
		}
	}
	return bases
}

// walkOpenRouterListing is the rule of a listing. It visits every model of models in order as (i, ""), and right after an OpenRouter chat model
// that is not itself a variant it visits (i, variant) for each routing variant the list does not already hold. Every list of entries and every
// listing by identity is the sequence of its visits, so they agree on which variants are listed and in which order.
func walkOpenRouterListing(models []GeneratedModel, visit func(i int, variant string)) {
	hasVariant := false
	for i := range models {
		if _, _, variant := OpenRouterRoutingVariant(models[i].Provider, models[i].ID); variant {
			hasVariant = true
			break
		}
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
	for i := range models {
		visit(i, "")
		if !isOpenRouterBase(&models[i]) {
			continue
		}
		for _, variant := range OpenRouterRoutingVariants {
			if hasVariant {
				if _, exists := listed[openRouterVariantID(models[i].ID, variant)]; exists {
					continue
				}
			}
			visit(i, variant)
		}
	}
}

// listOpenRouterRoutingVariants is the entries of a listing (walkOpenRouterListing): each model of models, and derive's entry for each routing
// variant listed after it. It returns models itself when it holds no model that takes variants.
func listOpenRouterRoutingVariants(models []GeneratedModel, derive func(base *GeneratedModel, variant string) GeneratedModel) []GeneratedModel {
	bases := openRouterBases(models)
	if bases == 0 {
		return models
	}
	out := make([]GeneratedModel, 0, len(models)+bases*len(OpenRouterRoutingVariants))
	walkOpenRouterListing(models, func(i int, variant string) {
		if variant == "" {
			out = append(out, models[i])
		} else {
			out = append(out, derive(&models[i], variant))
		}
	})
	return out
}

// ListedOpenRouterModel identifies one entry of a listing (WithOpenRouterRoutingVariants) without the entry: the id and name of the listed
// model, and the model of the list it is, or is a routing variant of. A variant repeats every other property of that model.
type ListedOpenRouterModel struct {
	// Base is the index in the listed models of the model the entry is, or is derived from.
	Base int32
	// Variant is 0 for a model of the list and, for a routing variant, one more than its index in OpenRouterRoutingVariants.
	Variant  int32
	ID, Name string
}

// VariantName is the routing variant the entry is, or "" for a model of the list.
func (m *ListedOpenRouterModel) VariantName() string {
	if m.Variant == 0 {
		return ""
	}
	return OpenRouterRoutingVariants[m.Variant-1]
}

// OpenRouterListing identifies the entries WithOpenRouterRoutingVariants(models) lists, in its order, and builds none of them. It returns nil
// when models holds no model that takes variants: the listing is then models itself. The listing of the bundled catalog of OpenRouter itself
// (bundledOpenRouterModels) is held and shared, so the caller reads it and never writes an element; an entry built later takes its id and
// name from it.
func OpenRouterListing(models []GeneratedModel) []ListedOpenRouterModel {
	if len(models) == 0 {
		return nil
	}
	if source := bundledOpenRouter(); len(models) == len(source) && &models[0] == &source[0] {
		return heldOpenRouterListing().entries
	}
	return identifyOpenRouterListing(models, func(base *GeneratedModel, variant string) (string, string) {
		return OpenRouterVariantIdentity(base.ID, base.DisplayName, variant)
	})
}

// identifyOpenRouterListing is the listing of models by identity (walkOpenRouterListing): each model of models, and each routing variant
// listed after it under the id and name identity gives it. It returns nil when models holds no model that takes variants.
func identifyOpenRouterListing(models []GeneratedModel, identity func(base *GeneratedModel, variant string) (id, name string)) []ListedOpenRouterModel {
	bases := openRouterBases(models)
	if bases == 0 {
		return nil
	}
	out := make([]ListedOpenRouterModel, 0, len(models)+bases*len(OpenRouterRoutingVariants))
	walkOpenRouterListing(models, func(i int, variant string) {
		if variant == "" {
			out = append(out, ListedOpenRouterModel{Base: int32(i), ID: models[i].ID, Name: models[i].DisplayName})
			return
		}
		id, name := identity(&models[i], variant)
		out = append(out, ListedOpenRouterModel{Base: int32(i), Variant: int32(slices.Index(OpenRouterRoutingVariants, variant)) + 1, ID: id, Name: name})
	})
	return slices.Clip(out)
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
			if hasVariant {
				if _, exists := listed[openRouterVariantID(chat.ID, variant)]; exists {
					continue
				}
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
	derived.ID, derived.DisplayName = OpenRouterVariantIdentity(m.ID, m.DisplayName, variant)
	if m.catalogRecord != "" {
		derived.catalogRecord = deriveCatalogRecord(m.catalogRecord, derived.ID, derived.DisplayName)
	}
	return &derived
}

// derivedVariantKey names the variant of a bundled OpenRouter model.
type derivedVariantKey struct{ base, variant string }

// derivedVariant is a variant derived from a bundled base entry, with the name and record of the base it was derived from.
type derivedVariant struct {
	baseName, baseRecord string
	entry                GeneratedModel
}

// matches reports whether v was derived from base as it is now.
func (v *derivedVariant) matches(base *GeneratedModel) bool {
	return v.baseName == base.DisplayName && v.baseRecord == base.Record
}

// openRouterBundle is the bundled catalog of OpenRouter with each model followed by its routing variants. It is built once, by the first listing, and
// never written: it is where a variant of a bundled model is held from then on, so a listing copies its entries, a lookup returns them and
// SharedOpenRouterRoutingVariants hands out models itself.
type openRouterBundle struct {
	// source is the bundled models the list was derived from.
	source []GeneratedModel
	models []GeneratedModel
	// at is the index in models of each model that takes routing variants, by id.
	at map[string]int32
}

// variant is the held variant of base, or nil when the bundle holds no such variant or was derived from a base with another name or record.
func (b *openRouterBundle) variant(base *GeneratedModel, variant string) *GeneratedModel {
	at, ok := b.at[base.ID]
	if !ok || b.models[at].DisplayName != base.DisplayName || b.models[at].Record != base.Record {
		return nil
	}
	for i := int(at) + 1; i < len(b.models) && i <= int(at)+len(OpenRouterRoutingVariants); i++ {
		id := b.models[i].ID
		if len(id) == len(base.ID)+1+len(variant) && id[len(base.ID)] == ':' && strings.HasPrefix(id, base.ID) && strings.HasSuffix(id, variant) {
			return &b.models[i]
		}
	}
	return nil
}

// openRouterListing is the listing by identity of the bundled catalog of OpenRouter (OpenRouterListing), held once it has been asked for.
type openRouterListing struct {
	// source is the bundled models the listing identifies.
	source  []GeneratedModel
	entries []ListedOpenRouterModel
}

// name is the id and name the listing holds for the routing variant of base, an element of source, searched from *from on. A walk of source
// asks in the listing's order, so each of its searches is one step.
func (l *openRouterListing) name(base *GeneratedModel, variant string, from *int) (id, name string, ok bool) {
	for i := *from; i < len(l.entries); i++ {
		entry := &l.entries[i]
		if entry.Variant != 0 && &l.source[entry.Base] == base && entry.VariantName() == variant {
			*from = i + 1
			return entry.ID, entry.Name, true
		}
	}
	return "", "", false
}

// derivedVariants holds what is derived from the bundled OpenRouter models, each part built once and never written. listing identifies every
// listed model and builds no variant. Before the first listing of entries a lookup derives the one variant it names and holds it in byKey; the
// first listing of entries builds bundle, which takes over those entries and holds every variant from then on. bySpec is the entry LookupModel
// and LookupModelExact found for a spec, so a repeated lookup returns one entry. byKey and bySpec are bounded by the catalog. Plain maps under
// a lock, not a sync.Map, which boxes a struct key on every read.
var derivedVariants struct {
	sync.RWMutex
	listing atomic.Pointer[openRouterListing]
	bundle  atomic.Pointer[openRouterBundle]
	byKey   map[derivedVariantKey]*derivedVariant
	bySpec  map[string]*GeneratedModel
}

// bundledOpenRouter is bundledOpenRouterModels, read once: the bundled catalog never changes.
var bundledOpenRouter = sync.OnceValue(bundledOpenRouterModels)

// heldOpenRouterListing returns the listing of the bundled catalog of OpenRouter, identifying it on the first call. A variant that is already
// built keeps its id and name.
func heldOpenRouterListing() *openRouterListing {
	if listing := derivedVariants.listing.Load(); listing != nil {
		return listing
	}
	derivedVariants.Lock()
	defer derivedVariants.Unlock()
	if listing := derivedVariants.listing.Load(); listing != nil {
		return listing
	}
	bundle := derivedVariants.bundle.Load()
	listing := &openRouterListing{source: bundledOpenRouter()}
	listing.entries = identifyOpenRouterListing(listing.source, func(base *GeneratedModel, variant string) (string, string) {
		if bundle != nil {
			if held := bundle.variant(base, variant); held != nil {
				return held.ID, held.DisplayName
			}
		}
		if single := derivedVariants.byKey[derivedVariantKey{base.ID, variant}]; single != nil && single.matches(base) {
			return single.entry.ID, single.entry.DisplayName
		}
		return OpenRouterVariantIdentity(base.ID, base.DisplayName, variant)
	})
	derivedVariants.listing.Store(listing)
	return listing
}

// deriveOpenRouterVariantLocked is base.OpenRouterVariant(variant) under the id and name the held listing gives the variant, when it holds
// them. from is where the listing is searched from (openRouterListing.name). The caller holds the write lock of derivedVariants.
func deriveOpenRouterVariantLocked(base *GeneratedModel, variant string, from *int) GeneratedModel {
	if listing := derivedVariants.listing.Load(); listing != nil {
		if id, name, ok := listing.name(base, variant, from); ok {
			return base.openRouterVariantNamed(id, name)
		}
	}
	return base.OpenRouterVariant(variant)
}

// bundledOpenRouterModels returns the bundled models of OpenRouter in catalog order: a window of GeneratedModels when the catalog lists them
// together, as the generated catalog does, and a copy of them otherwise.
func bundledOpenRouterModels() []GeneratedModel {
	count, first, last := 0, -1, -1
	for i := range GeneratedModels {
		if GeneratedModels[i].Provider == "openrouter" { //portlint:allow providerliteral the variants are OpenRouter's own routing suffixes
			count++
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if count == 0 {
		return nil
	}
	if count == last-first+1 {
		return GeneratedModels[first : last+1 : last+1]
	}
	models := make([]GeneratedModel, 0, count)
	for i := range GeneratedModels {
		if GeneratedModels[i].Provider == "openrouter" { //portlint:allow providerliteral the variants are OpenRouter's own routing suffixes
			models = append(models, GeneratedModels[i])
		}
	}
	return models
}

// heldOpenRouterBundle returns the bundle, building it on the first call. A variant a lookup derived earlier is copied into the list, so its id, name
// and record are built once, and the lookups that found it return the listed entry from then on.
func heldOpenRouterBundle() *openRouterBundle {
	if bundle := derivedVariants.bundle.Load(); bundle != nil {
		return bundle
	}
	derivedVariants.Lock()
	defer derivedVariants.Unlock()
	if bundle := derivedVariants.bundle.Load(); bundle != nil {
		return bundle
	}
	bundle := &openRouterBundle{source: bundledOpenRouter()}
	from := 0
	bundle.models = slices.Clip(listOpenRouterRoutingVariants(bundle.source, func(base *GeneratedModel, variant string) GeneratedModel {
		if single := derivedVariants.byKey[derivedVariantKey{base.ID, variant}]; single != nil && single.matches(base) {
			return single.entry
		}
		return deriveOpenRouterVariantLocked(base, variant, &from)
	}))
	bundle.at = make(map[string]int32, len(bundle.source))
	for i := range bundle.models {
		if isOpenRouterBase(&bundle.models[i]) {
			bundle.at[bundle.models[i].ID] = int32(i)
		}
	}
	if len(derivedVariants.byKey) > 0 {
		listed := make(map[*GeneratedModel]*GeneratedModel, len(derivedVariants.byKey))
		for key, single := range derivedVariants.byKey {
			at, ok := bundle.at[key.base]
			if !ok || !single.matches(&bundle.models[at]) {
				continue
			}
			if held := bundle.variant(&bundle.models[at], key.variant); held != nil {
				listed[&single.entry] = held
				delete(derivedVariants.byKey, key)
			}
		}
		for spec, entry := range derivedVariants.bySpec {
			if held, ok := listed[entry]; ok {
				derivedVariants.bySpec[spec] = held
			}
		}
		if len(derivedVariants.byKey) == 0 {
			// The list holds every variant now; the emptied map would keep its buckets.
			derivedVariants.byKey = nil
		}
	}
	derivedVariants.bundle.Store(bundle)
	return bundle
}

// heldOpenRouterVariantLocked is base.OpenRouterVariant(variant), held once for a base that is a bundled model and derived afresh for any other:
// the listed entry once the bundle is built, an entry of byKey before that. The caller holds the write lock of derivedVariants.
func heldOpenRouterVariantLocked(base *GeneratedModel, variant string) *GeneratedModel {
	if bundle := derivedVariants.bundle.Load(); bundle != nil {
		if held := bundle.variant(base, variant); held != nil {
			return held
		}
	}
	key := derivedVariantKey{base.ID, variant}
	single := derivedVariants.byKey[key]
	if single == nil {
		from := 0
		single = &derivedVariant{baseName: base.DisplayName, baseRecord: base.Record, entry: deriveOpenRouterVariantLocked(base, variant, &from)}
		if derivedVariants.byKey == nil {
			derivedVariants.byKey = map[derivedVariantKey]*derivedVariant{}
		}
		derivedVariants.byKey[key] = single
	}
	if single.matches(base) {
		return &single.entry
	}
	entry := base.OpenRouterVariant(variant)
	return &entry
}

// HeldOpenRouterVariant is the routing variant of base, an entry of the bundled catalog: the entry LookupModelExact returns for it, built on
// the first call that names it. A variant of any other entry is derived afresh.
func HeldOpenRouterVariant(base *GeneratedModel, variant string) *GeneratedModel {
	if bundle := derivedVariants.bundle.Load(); bundle != nil {
		if held := bundle.variant(base, variant); held != nil {
			return held
		}
	}
	derivedVariants.RLock()
	single := derivedVariants.byKey[derivedVariantKey{base.ID, variant}]
	derivedVariants.RUnlock()
	if single != nil && single.matches(base) {
		return &single.entry
	}
	derivedVariants.Lock()
	defer derivedVariants.Unlock()
	return heldOpenRouterVariantLocked(base, variant)
}

// lookupOpenRouterVariant resolves spec, "<base>:<variant>", from the entry base finds for "<base>"; a base that is no OpenRouter
// entry that takes variants is no match. It is called only for a spec the catalog does not list.
func lookupOpenRouterVariant(spec string, base func(string) (*GeneratedModel, bool)) (*GeneratedModel, bool) {
	cut := strings.LastIndexByte(spec, ':')
	if cut < 0 || !slices.Contains(OpenRouterRoutingVariants, spec[cut+1:]) {
		return nil, false
	}
	derivedVariants.RLock()
	cached := derivedVariants.bySpec[spec]
	derivedVariants.RUnlock()
	if cached != nil {
		return cached, true
	}
	entry, ok := base(spec[:cut])
	if !ok || !isOpenRouterBase(entry) {
		return nil, false
	}
	derivedVariants.Lock()
	defer derivedVariants.Unlock()
	if cached := derivedVariants.bySpec[spec]; cached != nil {
		return cached, true
	}
	derived := heldOpenRouterVariantLocked(entry, spec[cut+1:])
	if derivedVariants.bySpec == nil {
		derivedVariants.bySpec = map[string]*GeneratedModel{}
	}
	derivedVariants.bySpec[spec] = derived
	return derived, true
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
