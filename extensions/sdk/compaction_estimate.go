package sdk

import (
	"math"
	"strconv"
	"strings"
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

// utf16Length is a JavaScript string's length: the UTF-16 code units of the text. A byte that is not UTF-8 counts as the one unit of U+FFFD.
func utf16Length(text string) int {
	units := 0
	for _, r := range text {
		units++
		if r >= 0x10000 {
			units++
		}
	}
	return units
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

// jsonQuotedLength is the UTF-16 length of JSON.stringify(text): quotes, the short escapes, \u00XX for the other control characters, and every other
// character as it is (U+2028 and U+2029 included).
func jsonQuotedLength(text string, standIns map[rune]bool) int {
	length := 2
	for _, r := range text {
		switch {
		case r == '"' || r == '\\' || r == '\b' || r == '\f' || r == '\n' || r == '\r' || r == '\t':
			length += 2
		case r < 0x20 || standIns[r]:
			length += 6
		case r >= 0x10000:
			length += 2
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
