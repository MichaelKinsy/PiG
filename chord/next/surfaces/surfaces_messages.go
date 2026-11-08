package surfaces

import (
	"fmt"
	"reflect"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/src/surfaces/messages.ts. A plugin defines its texts once, per namespace: DefineMessages takes
// the catalogs by locale, the first defining the keys. A Messages renders in a language: bound to one (In), or plain,
// in the first catalog. Message builds a message as data (`{namespace, key, params}`) for state, notices and errors,
// which each client renders with Render in its own language, checked at run time (it crossed a link).

// MessageFunc is a message that is a function of its arguments (plurals, word order). Its language is the one the
// message renders in, nil outside a render (D450: Pi's current language is this parameter). It returns the error a
// JavaScript message would throw (its arguments do not fit).
type MessageFunc func(lang *Language, args ...any) (string, error)

// MessageValue is a message's value: a string, or a MessageFunc.
type MessageValue any

// Catalog is the messages of one namespace in one language, by key.
type Catalog map[string]MessageValue

// LocaleCatalog is one catalog of DefineMessages with its locale, in the order the plugin lists them (the first defines
// the keys).
type LocaleCatalog struct {
	Locale  string
	Catalog Catalog
}

// MessageData is a message as data: its namespace, its key and its parameters; plain JSON (strings, numbers, booleans,
// nil), so it crosses links.
type MessageData struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Params    []any  `json:"params"`
}

// MessageSchema is the schema of a message as data, for contract members.
var MessageSchema = contracts.Obj(
	contracts.Prop("namespace", contracts.Str()),
	contracts.Prop("key", contracts.Str()),
	contracts.Prop("params", contracts.Arr(contracts.Union([]contracts.Schema{
		contracts.Str(), contracts.Num(), contracts.Bool(), contracts.Nul(),
	}))),
)

// Messages is a plugin's messages: its namespace, its catalogs as contributions, and the renderings.
type Messages struct {
	Namespace string
	// Translations are its catalogs as `Translations` entries: a facet that renders contributes them.
	Translations []Translation
	own          map[string]Catalog
	first        Catalog
}

// DefineMessages is the messages of namespace (a name like a type's, `[a-z0-9@/._~-]`) in catalogs by locale; the first
// catalog defines the keys.
func DefineMessages(namespace string, catalogs ...LocaleCatalog) (*Messages, error) {
	// Declaration policy: a namespace is a name, a catalog's key a language tag.
	if err := contracts.CheckName(namespace, "A messages namespace"); err != nil {
		return nil, err
	}
	m := &Messages{Namespace: namespace, own: map[string]Catalog{}}
	var order []string
	for _, entry := range catalogs {
		canonical, ok := CanonicalLocale(entry.Locale)
		if !ok {
			return nil, fmt.Errorf("%s: %q is no language tag", namespace, entry.Locale)
		}
		if _, seen := m.own[canonical]; !seen {
			order = append(order, canonical)
		}
		m.own[canonical] = entry.Catalog
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("%s: defineMessages needs a catalog", namespace)
	}
	m.first = m.own[order[0]]
	for _, locale := range order {
		m.Translations = append(m.Translations, Translation{Namespace: namespace, Locale: locale, Messages: m.own[locale]})
	}
	return m, nil
}

// T renders the message key with its arguments in the first catalog (Pi's `t(...)` outside a render).
func (m *Messages) T(key string, args ...any) (string, error) { return m.In(nil).T(key, args...) }

// Render renders a message as data in the first catalog.
func (m *Messages) Render(message MessageData) string { return m.In(nil).Render(message) }

// Message is the message key with its arguments as data, for state that clients render.
func (m *Messages) Message(key string, params ...any) MessageData {
	return MessageData{Namespace: m.Namespace, Key: key, Params: append([]any{}, params...)}
}

// Translator renders the messages of one namespace in one language.
type Translator struct {
	messages *Messages
	language *Language
}

// In is `t` in language (nil: the first catalog).
func (m *Messages) In(language *Language) *Translator {
	return &Translator{messages: m, language: language}
}

// T renders the message key with its arguments: down the language's chain, at each locale the host's contributed
// catalog, then the plugin's own; else the first catalog. A key the first catalog lacks is an error.
func (t *Translator) T(key string, args ...any) (string, error) {
	return t.messages.renderKey(t.language, key, args)
}

// Render renders a message as data. Checked first (wire input: the message crossed a link as state): another
// namespace, a key the first catalog lacks or params that fit no message show the key, reported.
func (t *Translator) Render(message MessageData) string {
	return t.messages.renderData(t.language, message)
}

func (m *Messages) renderKey(language *Language, key string, args []any) (string, error) {
	if language == nil {
		return call(nil, m.first, key, args)
	}
	for index, locale := range language.Chain {
		contributed, hasContributed := language.Translations.Catalogs[m.Namespace][locale]
		if hasContributed {
			if _, ok := contributed.Value.Messages[key]; ok {
				text, err := call(language, contributed.Value.Messages, key, args)
				if err == nil {
					return m.found(language, key, index, text), nil
				}
				// A contributed catalog is a plugin's code: reported, and the chain goes on (design §17).
				language.Report(MessagesReport{
					Kind: ReportFailed, Namespace: m.Namespace, Locale: locale, Key: key, Plugin: contributed.Plugin, Error: err,
				})
			}
		}
		own, hasOwn := m.own[locale]
		if hasOwn && (!hasContributed || !sameCatalog(own, contributed.Value.Messages)) {
			if _, ok := own[key]; ok {
				text, err := call(language, own, key, args)
				if err != nil {
					return "", err
				}
				return m.found(language, key, index, text), nil
			}
		}
	}
	text, err := call(language, m.first, key, args)
	if err != nil {
		return "", err
	}
	return m.found(language, key, len(language.Chain), text), nil
}

// found is text, found at index in language's chain (its length: the first catalog), a fallback reported past the
// first.
func (m *Messages) found(language *Language, key string, index int, text string) string {
	if index > 0 {
		used := ""
		if index < len(language.Chain) {
			used = language.Chain[index]
		}
		language.Report(MessagesReport{Kind: ReportFallback, Namespace: m.Namespace, Locale: language.Locale, Key: key, Used: used})
	}
	return text
}

func (m *Messages) renderData(language *Language, message MessageData) string {
	_, known := m.first[message.Key]
	if message.Namespace != m.Namespace || !known || message.Params == nil {
		if language != nil {
			language.Report(MessagesReport{Kind: ReportUnknown, Namespace: message.Namespace, Key: message.Key, Expected: m.Namespace})
		}
		return message.Key
	}
	text, err := m.renderKey(language, message.Key, message.Params)
	if err != nil {
		if language != nil {
			language.Report(MessagesReport{Kind: ReportFailed, Namespace: message.Namespace, Locale: language.Locale, Key: message.Key, Error: err})
		}
		return message.Key
	}
	return text
}

// call is a message's text: the text, or the function called with args. A panic is the error a throw would be; a key
// the catalog lacks is the TypeError JavaScript throws calling `undefined`.
func call(language *Language, catalog Catalog, key string, args []any) (text string, err error) {
	value, ok := catalog[key]
	if !ok {
		return "", fmt.Errorf("%s is not a message", key)
	}
	switch v := value.(type) {
	case string:
		return v, nil
	case MessageFunc:
		defer func() {
			if recovered := recover(); recovered != nil {
				text, err = "", fmt.Errorf("message %s panicked: %v", key, recovered)
			}
		}()
		return v(language, args...)
	default:
		return "", fmt.Errorf("message %s is a %T: a string or a MessageFunc", key, value)
	}
}

// sameCatalog is JavaScript's identity of two catalogs: one map.
func sameCatalog(a, b Catalog) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}
