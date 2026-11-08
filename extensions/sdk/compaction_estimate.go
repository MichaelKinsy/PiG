package sdk

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// EstimateTokens is Pi's estimateTokens (core/compaction/compaction.ts): a chars/4 heuristic over one agent message as the wire carries it (a decoded JSON
// object with a "role"), rounded up. Lengths are JavaScript string lengths, UTF-16 code units, and a tool call's arguments count as the length of their
// JSON.stringify text. A role Pi does not size is 0.
func EstimateTokens(message map[string]any) int {
	return estimateTokens(message, nil)
}

// estimateTokens is [EstimateTokens] for a message read from text that held unpaired surrogates ([protectSurrogates]): each rune of standIns stands for one, which
// is one unit in a string and the six of "\ud800" in the JSON.stringify text of a tool call's arguments.
func estimateTokens(message map[string]any, standIns map[rune]bool) int {
	chars := 0
	switch role, _ := message["role"].(string); role {
	case "system":
		chars = estimateContentChars(message["content"])
		if sections, ok := message["sections"].(map[string]any); ok {
			for _, section := range sections {
				if text, ok := section.(string); ok {
					chars += utf16Length(text)
				}
			}
		}
		if added := message["toolsAdded"]; jsTruthy(added) {
			chars += jsonStringifyLength(added, standIns)
		}
	case "user", "custom", "toolResult":
		chars = estimateContentChars(message["content"])
	case "assistant":
		blocks, _ := message["content"].([]any)
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			switch kind, _ := block["type"].(string); kind {
			case "text":
				text, _ := block["text"].(string)
				chars += utf16Length(text)
			case "thinking":
				thinking, _ := block["thinking"].(string)
				chars += utf16Length(thinking)
			case "toolCall":
				name, _ := block["name"].(string)
				chars += utf16Length(name) + jsonStringifyLength(block["arguments"], standIns)
			}
		}
	case "bashExecution":
		command, _ := message["command"].(string)
		output, _ := message["output"].(string)
		chars = utf16Length(command) + utf16Length(output)
	case "branchSummary", "compactionSummary":
		summary, _ := message["summary"].(string)
		chars = utf16Length(summary)
	default:
		return 0
	}
	return int(math.Ceil(float64(chars) / 4))
}

func jsTruthy(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case float64:
		return value != 0 && !math.IsNaN(value)
	}
	return true
}

// estimatedImageChars is Pi's ESTIMATED_IMAGE_CHARS.
const estimatedImageChars = 4800

func estimateContentChars(content any) int {
	switch content := content.(type) {
	case string:
		return utf16Length(content)
	case []any:
		chars := 0
		for _, raw := range content {
			block, _ := raw.(map[string]any)
			switch kind, _ := block["type"].(string); kind {
			case "text":
				if text, _ := block["text"].(string); text != "" {
					chars += utf16Length(text)
				}
			case "image":
				chars += estimatedImageChars
			}
		}
		return chars
	}
	return 0
}

// utf16Length is a JavaScript string's length: the UTF-16 code units of the text. The SDK holds an unpaired surrogate as WTF-8 (the three bytes of its code point,
// extensions/sdk/json), which is the one unit the JavaScript string had; any other byte that is not UTF-8 counts as the one unit of U+FFFD. JavaScript reads the
// length of a string in constant time, so an ASCII prefix, one unit per byte, is skipped eight bytes at a time and only the rest is decoded.
func utf16Length(text string) int {
	prefix := 0
	for ; prefix+8 <= len(text); prefix += 8 {
		if text[prefix]|text[prefix+1]|text[prefix+2]|text[prefix+3]|text[prefix+4]|text[prefix+5]|text[prefix+6]|text[prefix+7] >= utf8.RuneSelf {
			break
		}
	}
	for prefix < len(text) && text[prefix] < utf8.RuneSelf {
		prefix++
	}
	units := prefix
	rest := text[prefix:]
	for _, r := range rest {
		units++
		if r >= 0x10000 {
			units++
		}
	}
	// range read the three bytes of each WTF-8 surrogate as three invalid bytes, three units where the surrogate is one. Only a lead byte 0xED can start one, so a text with
	// none (almost every text) costs this one vectorised search.
	for {
		at := strings.IndexByte(rest, 0xED)
		if at < 0 {
			break
		}
		if wtf8Surrogate(rest[at:]) {
			units -= 2
			at += 2
		}
		rest = rest[at+1:]
	}
	return units
}

// wtf8Surrogate reports whether text starts with the three bytes of a surrogate code point, U+D800 to U+DFFF: the form the SDK holds an unpaired surrogate in. UTF-8 has
// no such sequence, so utf8.DecodeRuneInString reads it as three invalid bytes.
func wtf8Surrogate(text string) bool {
	return len(text) >= 3 && text[0] == 0xED && text[1] >= 0xA0 && text[1] <= 0xBF && text[2]&0xC0 == 0x80
}

// jsonStringifyLength is the UTF-16 length of JSON.stringify(value) for a decoded JSON value, without building the text. A key order does not change a
// length, so the map's iteration order is irrelevant.
func jsonStringifyLength(value any, standIns map[rune]bool) int {
	switch value := value.(type) {
	case nil:
		return len("null")
	case bool:
		if value {
			return len("true")
		}
		return len("false")
	case float64:
		return len(jsNumberString(value))
	case string:
		return jsonQuotedLength(value, standIns)
	case []any:
		length := 2
		for i, element := range value {
			if i > 0 {
				length++
			}
			length += jsonStringifyLength(element, standIns)
		}
		return length
	case map[string]any:
		length := 2
		first := true
		for key, element := range value {
			if !first {
				length++
			}
			first = false
			length += jsonQuotedLength(key, standIns) + 1 + jsonStringifyLength(element, standIns)
		}
		return length
	}
	return 0
}

// jsonQuotedLength is the UTF-16 length of JSON.stringify(text): quotes, the short escapes, \u00XX for the other control characters and \uXXXX for an unpaired
// surrogate, and every other character as it is (U+2028 and U+2029 included).
func jsonQuotedLength(text string, standIns map[rune]bool) int {
	length := 2
	for i, r := range text {
		switch {
		case r == '"' || r == '\\' || r == '\b' || r == '\f' || r == '\n' || r == '\r' || r == '\t':
			length += 2
		case r < 0x20 || standIns[r]:
			length += 6
		case r >= 0x10000:
			length += 2
		case r == utf8.RuneError && wtf8Surrogate(text[i:]):
			// JSON.stringify writes an unpaired surrogate as its six-character escape; range reads its three bytes as three invalid bytes, each counting one.
			length += 6 - 3 + 1
		default:
			length++
		}
	}
	return length
}

// jsNumberString is JavaScript's Number::toString for the number JSON.stringify writes: the shortest digits that round-trip, positional for exponents from
// -7 to 20 and exponential beyond; -0 is "0" and a non-finite number is "null".
func jsNumberString(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "null"
	}
	if value == 0 {
		return "0"
	}
	sign := ""
	if value < 0 {
		sign, value = "-", -value
	}
	exponential := strconv.FormatFloat(value, 'e', -1, 64)
	mantissa, exponentText, _ := strings.Cut(exponential, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	exponent, _ := strconv.Atoi(exponentText)
	n, k := exponent+1, len(digits)
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	exponentSign := "+"
	if n-1 < 0 {
		exponentSign = "-"
	}
	magnitude := strconv.Itoa(int(math.Abs(float64(n - 1))))
	if k == 1 {
		return sign + digits + "e" + exponentSign + magnitude
	}
	return sign + digits[:1] + "." + digits[1:] + "e" + exponentSign + magnitude
}
