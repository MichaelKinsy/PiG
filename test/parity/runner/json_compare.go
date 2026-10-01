//go:build parity

package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// JSONAliasRule identifies scalar identities at explicit JSON pointer patterns. * matches one segment; ** matches zero or more. No field is removed.
type JSONAliasRule struct {
	Paths   []string `toml:"paths"`
	Kind    string   `toml:"kind"`    // id, timestamp, duration, path, literal, or session_file
	Pig     string   `toml:"pig"`     // exact Pig spelling for a literal alias
	Pi      string   `toml:"pi"`      // exact Pi spelling for a literal alias
	Roots   []string `toml:"roots"`   // selected runtime roots; empty retains the standard directory roots
	Group   string   `toml:"group"`   // shared identity namespace for ids and references
	Pattern string   `toml:"pattern"` // optional scalar eligibility regex; both sides must match
	Reason  string   `toml:"reason"`
}

type identityPair struct{ forward, reverse map[string]string }

func compareJSONResults(g, p Result, rules []JSONAliasRule) error {
	if g.RawOutput != "" {
		g.Output = g.RawOutput
	}
	if p.RawOutput != "" {
		p.Output = p.RawOutput
	}
	left, err := parseJSONRecords(g.Output)
	if err != nil {
		return fmt.Errorf("pig: %w", err)
	}
	right, err := parseJSONRecords(p.Output)
	if err != nil {
		return fmt.Errorf("pi: %w", err)
	}
	pairs := map[string]*identityPair{}
	for _, rule := range rules {
		if rule.Reason == "" || len(rule.Paths) == 0 {
			return fmt.Errorf("JSON alias requires paths and reason")
		}
		if rule.Kind != "id" && rule.Kind != "timestamp" && rule.Kind != "duration" && rule.Kind != "path" && rule.Kind != "literal" && rule.Kind != "session_file" {
			return fmt.Errorf("unknown JSON alias kind %q", rule.Kind)
		}
		if rule.Kind == "literal" && (rule.Pig == "" || rule.Pi == "") {
			return fmt.Errorf("literal alias requires nonempty pig and pi spellings")
		}
		if (rule.Kind == "id" || rule.Kind == "session_file") && rule.Group == "" {
			return fmt.Errorf("identity alias requires group")
		}
		if rule.Pattern != "" {
			if _, err := regexp.Compile(rule.Pattern); err != nil {
				return err
			}
		}
	}
	var compare func(string, any, any) error
	compare = func(path string, a, b any) error {
		gRoots, pRoots := map[string]string{}, map[string]string{}
		gLiterals, pLiterals := map[string]string{}, map[string]string{}
		for index, rule := range rules {
			if !aliasMatches(rule, path, a, b) {
				continue
			}
			switch rule.Kind {
			case "timestamp":
				if validTimestamp(a) && validTimestamp(b) && fmt.Sprintf("%T", a) == fmt.Sprintf("%T", b) {
					return nil
				}
			case "duration":
				if validDuration(a) && validDuration(b) {
					return nil
				}
			case "path":
				if err := addAliasRoots(gRoots, g.IdentityRoots, rule.Roots); err != nil {
					return err
				}
				if err := addAliasRoots(pRoots, p.IdentityRoots, rule.Roots); err != nil {
					return err
				}
			case "literal":
				key := fmt.Sprint(index)
				gLiterals[key], pLiterals[key] = rule.Pig, rule.Pi
			case "session_file":
				x, xok := a.(string)
				y, yok := b.(string)
				if xok && yok {
					return compareSessionFilename(path, x, y, g, p, rule, pairs)
				}
			case "id":
				x, xok := a.(string)
				y, yok := b.(string)
				if xok && yok && x != "" && y != "" {
					return compareIdentity(path, x, y, rule.Group, pairs)
				}
			}
		}
		if x, ok := a.(string); ok {
			if y, ok := b.(string); ok && slices.Equal(aliasText(x, gRoots, gLiterals), aliasText(y, pRoots, pLiterals)) {
				return nil
			}
		}
		switch x := a.(type) {
		case map[string]any:
			y, ok := b.(map[string]any)
			if !ok {
				break
			}
			keys := make([]string, 0, len(x)+len(y))
			for key := range x {
				keys = append(keys, key)
			}
			for key := range y {
				if _, ok := x[key]; !ok {
					keys = append(keys, key)
				}
			}
			slices.Sort(keys)
			for _, key := range keys {
				av, aok := x[key]
				bv, bok := y[key]
				next := path + "/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(key)
				if aok != bok {
					return fmt.Errorf("%s: field presence differs (pig=%t pi=%t)", next, aok, bok)
				}
				if err := compare(next, av, bv); err != nil {
					return err
				}
			}
			return nil
		case []any:
			y, ok := b.([]any)
			if !ok {
				break
			}
			for i := range min(len(x), len(y)) {
				if err := compare(fmt.Sprintf("%s/%d", path, i), x[i], y[i]); err != nil {
					return err
				}
			}
			if len(x) != len(y) {
				return fmt.Errorf("%s: array/record count pig=%d pi=%d", path, len(x), len(y))
			}
			return nil
		case json.Number:
			y, ok := b.(json.Number)
			if ok {
				xn, xok := new(big.Rat).SetString(string(x))
				yn, yok := new(big.Rat).SetString(string(y))
				if xok && yok && xn.Cmp(yn) == 0 {
					return nil
				}
			}
		default:
			// Scalar types remain distinct: null, false, zero and empty string never coalesce.
			if fmt.Sprintf("%T", a) == fmt.Sprintf("%T", b) && a == b {
				return nil
			}
		}
		return fmt.Errorf("%s: pig=%s pi=%s", path, jsonDiagnostic(a), jsonDiagnostic(b))
	}
	return compare("", left, right)
}

func jsonDiagnostic(value any) string {
	data, _ := json.Marshal(value)
	return trunc(string(data), 500)
}

func validTimestamp(value any) bool {
	switch v := value.(type) {
	case json.Number:
		_, err := v.Int64()
		return err == nil
	case string:
		_, err := time.Parse(time.RFC3339Nano, v)
		return err == nil
	default:
		return false
	}
}

// decisecondSpelling is the JSON spelling of Math.round(ms / 100) / 10 (bash.ts:392): a non-negative decimal with at most one fractional digit.
var decisecondSpelling = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9])?$`)

// validDuration accepts a measured elapsed time in Pi's 0.1 s steps. Other types, negative values and unrounded or exponent spellings stay distinct.
func validDuration(value any) bool {
	number, ok := value.(json.Number)
	return ok && decisecondSpelling.MatchString(string(number))
}

func aliasMatches(rule JSONAliasRule, path string, a, b any) bool {
	matched := false
	for _, pattern := range rule.Paths {
		if pointerMatches(strings.Split(pattern, "/"), strings.Split(path, "/")) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	if rule.Pattern != "" {
		x, xok := a.(string)
		y, yok := b.(string)
		re := regexp.MustCompile(rule.Pattern)
		return xok && yok && re.MatchString(x) && re.MatchString(y)
	}
	return true
}

func pointerMatches(pattern, path []string) bool {
	if len(pattern) == 0 {
		return len(path) == 0
	}
	if pattern[0] == "**" {
		return pointerMatches(pattern[1:], path) || (len(path) > 0 && pointerMatches(pattern, path[1:]))
	}
	return len(path) > 0 && (pattern[0] == "*" || pattern[0] == path[0]) && pointerMatches(pattern[1:], path[1:])
}

func compareIdentity(path, pig, pi, group string, pairs map[string]*identityPair) error {
	pair := pairs[group]
	if pair == nil {
		pair = &identityPair{map[string]string{}, map[string]string{}}
		pairs[group] = pair
	}
	if old, ok := pair.forward[pig]; ok && old != pi {
		return fmt.Errorf("%s: identity reference changed: pig=%q pi=%q (previous pi=%q)", path, pig, pi, old)
	}
	if old, ok := pair.reverse[pi]; ok && old != pig {
		return fmt.Errorf("%s: identity reuse: pig=%q pi=%q (previous pig=%q)", path, pig, pi, old)
	}
	pair.forward[pig], pair.reverse[pi] = pi, pig
	return nil
}

func addAliasRoots(target, roots map[string]string, names []string) error {
	if len(names) == 0 {
		for key, value := range roots {
			// Documentation file/URL identities require explicit selection; existing directory aliases must not broaden when metadata gains a destination.
			if key == "readme" || key == "examples" {
				continue
			}
			target[key] = value
		}
		return nil
	}
	for _, name := range names {
		value := roots[name]
		if value == "" {
			return fmt.Errorf("JSON path alias root %q is unavailable", name)
		}
		target[name] = value
	}
	return nil
}

var sessionFilenamePattern = regexp.MustCompile(`^([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}-[0-9]{3}Z)_([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)

// Pi core/session-manager.ts:newSession spells filenames as an ISO millisecond clock followed by the session ID. Preserve directories and bind the embedded ID to the declared session group.
func compareSessionFilename(path, pig, pi string, g, p Result, rule JSONAliasRule, pairs map[string]*identityPair) error {
	split := func(value string) (string, string, error) {
		start := strings.LastIndexAny(value, "/\\") + 1
		parts := sessionFilenamePattern.FindStringSubmatch(value[start:])
		if parts == nil {
			return "", "", fmt.Errorf("%s: invalid session filename %q", path, value)
		}
		stamp := parts[1]
		iso := stamp[:11] + strings.ReplaceAll(stamp[11:19], "-", ":") + "." + stamp[20:]
		if _, err := time.Parse(time.RFC3339Nano, iso); err != nil {
			return "", "", fmt.Errorf("%s: invalid session filename timestamp: %w", path, err)
		}
		return value[:start], parts[2], nil
	}
	gd, gi, err := split(pig)
	if err != nil {
		return err
	}
	pd, piID, err := split(pi)
	if err != nil {
		return err
	}
	gr, pr := map[string]string{}, map[string]string{}
	if err := addAliasRoots(gr, g.IdentityRoots, rule.Roots); err != nil {
		return err
	}
	if err := addAliasRoots(pr, p.IdentityRoots, rule.Roots); err != nil {
		return err
	}
	if !slices.Equal(aliasText(gd, gr, nil), aliasText(pd, pr, nil)) {
		return fmt.Errorf("%s: session directories differ: pig=%q pi=%q", path, gd, pd)
	}
	return compareIdentity(path, gi, piID, rule.Group, pairs)
}

type pathSegment struct{ literal, root string }

type textIdentity struct {
	value, key   string
	pathBoundary bool
}

func aliasText(value string, roots, literals map[string]string) []pathSegment {
	// Typed segments prevent literal text such as "<cwd>" from impersonating an alias. Scan original text once, so replacements cannot cascade into other rules.
	identities := make([]textIdentity, 0, len(roots)+len(literals))
	for key, value := range roots {
		identities = append(identities, textIdentity{value, "root:" + key, true})
	}
	for key, value := range literals {
		identities = append(identities, textIdentity{value, "literal:" + key, false})
	}
	slices.SortFunc(identities, func(a, b textIdentity) int {
		if len(a.value) != len(b.value) {
			return len(b.value) - len(a.value)
		}
		return strings.Compare(a.key, b.key)
	})
	var result []pathSegment
	start := 0
	for i := 0; i < len(value); i++ {
		for _, identity := range identities {
			if identity.value == "" || !strings.HasPrefix(value[i:], identity.value) {
				continue
			}
			end := i + len(identity.value)
			if identity.pathBoundary {
				if i > 0 && !strings.ContainsAny(value[i-1:i], " \t\n\r<>\"'") {
					continue
				}
				if end < len(value) && !strings.ContainsAny(value[end:end+1], "/\\\n\r<>\"' )") {
					continue
				}
			}
			if i > start {
				result = append(result, pathSegment{literal: value[start:i]})
			}
			result = append(result, pathSegment{root: identity.key})
			start, i = end, end-1
			break
		}
	}
	if start < len(value) {
		result = append(result, pathSegment{literal: value[start:]})
	}
	return result
}

func resultIdentityRoots(cwd, temp string, env []string) map[string]string {
	roots := map[string]string{"cwd": cwd, "temp": temp}
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		switch key {
		case "PIG_HOME":
			roots["agent"] = value + "/agent"
			roots["docs"] = value + "/docs"
			roots["readme"] = value + "/docs/README.md"
			roots["examples"] = "https://github.com/MichaelKinsy/PiG/tree/main/examples"
		case "PI_PACKAGE_DIR":
			roots["docs"] = value + "/docs"
			roots["readme"] = value + "/README.md"
			roots["examples"] = value + "/examples"
		case "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR":
			roots["agent"] = value
		}
	}
	return roots
}

func parseJSONRecords(output string) ([]any, error) {
	if output == "" {
		return nil, fmt.Errorf("empty JSONL output")
	}
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	records := make([]any, 0, len(lines))
	for i, line := range lines {
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		value, err := decodeJSONValue(decoder, line)
		if err != nil {
			return nil, fmt.Errorf("JSONL record %d: %w", i+1, err)
		}
		if _, err := decoder.Token(); err != io.EOF {
			return nil, fmt.Errorf("JSONL record %d has trailing data", i+1)
		}
		if _, ok := value.(map[string]any); !ok {
			return nil, fmt.Errorf("JSONL record %d is not an object", i+1)
		}
		records = append(records, value)
	}
	return records, nil
}

func decodeJSONValue(d *json.Decoder, source string) (any, error) {
	token, err := readJSONToken(d, source)
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		value := map[string]any{}
		for d.More() {
			key, err := readJSONToken(d, source)
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("object key is not a string")
			}
			if _, ok := value[name]; ok {
				return nil, fmt.Errorf("duplicate JSON key %q", name)
			}
			child, err := decodeJSONValue(d, source)
			if err != nil {
				return nil, err
			}
			value[name] = child
		}
		_, err := d.Token()
		return value, err
	case json.Delim('['):
		value := []any{}
		for d.More() {
			child, err := decodeJSONValue(d, source)
			if err != nil {
				return nil, err
			}
			value = append(value, child)
		}
		_, err := d.Token()
		return value, err
	default:
		return token, nil
	}
}

func readJSONToken(d *json.Decoder, source string) (any, error) {
	start := d.InputOffset()
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	if value, ok := token.(string); ok {
		raw := strings.TrimLeft(source[start:d.InputOffset()], ",: \t\r\n")
		return jsonStringIdentity(raw, value), nil
	}
	return token, nil
}

// jsonStringIdentity retains lone UTF-16 units as WTF-8 bytes inside comparison strings only. encoding/json has already validated the token; its replacement-character decoding must not collapse distinct JSON strings or keys.
func jsonStringIdentity(raw, decoded string) string {
	if !strings.Contains(raw, `\u`) {
		return decoded
	}
	out := make([]byte, 0, len(decoded))
	for i := 1; i < len(raw)-1; {
		if raw[i] != '\\' {
			r, size := utf8.DecodeRuneInString(raw[i:])
			out = utf8.AppendRune(out, r)
			i += size
			continue
		}
		escape := raw[i+1]
		i += 2
		if escape != 'u' {
			switch escape {
			case 'b':
				escape = '\b'
			case 'f':
				escape = '\f'
			case 'n':
				escape = '\n'
			case 'r':
				escape = '\r'
			case 't':
				escape = '\t'
			}
			out = append(out, escape)
			continue
		}
		value, _ := strconv.ParseUint(raw[i:i+4], 16, 16)
		i += 4
		if value >= 0xd800 && value <= 0xdbff && i+6 <= len(raw)-1 && raw[i:i+2] == `\u` {
			low, _ := strconv.ParseUint(raw[i+2:i+6], 16, 16)
			if low >= 0xdc00 && low <= 0xdfff {
				out = utf8.AppendRune(out, utf16.DecodeRune(rune(value), rune(low)))
				i += 6
				continue
			}
		}
		if utf16.IsSurrogate(rune(value)) {
			out = append(out, byte(0xe0|value>>12), byte(0x80|(value>>6)&0x3f), byte(0x80|value&0x3f))
		} else {
			out = utf8.AppendRune(out, rune(value))
		}
	}
	return string(out)
}
