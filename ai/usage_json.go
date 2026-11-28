package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonwire"
)

// MarshalJSON emits the upstream Usage wire shape. A provider creates cacheWrite1h and reasoning on a usage object that already holds
// totalTokens and cost (anthropic-messages.ts:684, :846-851, bedrock-converse-stream.ts:715), so JSON.stringify writes them last; usage
// built as one literal (usage-totals.ts combineUsage) holds them before totalTokens.
func (usage Usage) MarshalJSON() ([]byte, error) {
	var encoded []byte
	var err error
	if usage.splitsAfterCost {
		encoded, err = json.Marshal(usage.wireSplitsAfterCost())
	} else {
		encoded, err = json.Marshal(usage.wire())
	}
	if err != nil {
		return nil, err
	}
	// JSON.stringify writes a negative zero as 0, as MarshalJSONTo does.
	if jsonwire.MayHaveJSForms(encoded) {
		return sdkjson.AppendJSNormalized(encoded[:0], encoded)
	}
	return encoded, nil
}

// AppendStringify appends the JSON.stringify text of MarshalJSON's output to dst (sdkjson.StringifyAppender). A non-finite cost is the error MarshalJSON reports.
func (usage Usage) AppendStringify(dst []byte) ([]byte, error) {
	cost := usage.Cost
	for _, f := range [...]float64{cost.Input, cost.Output, cost.CacheRead, cost.CacheWrite, cost.Total} {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			_, err := usage.MarshalJSON()
			return dst, err
		}
	}
	if usage.splitsAfterCost {
		return sdkjson.AppendStringify(dst, usage.wireSplitsAfterCost())
	}
	return sdkjson.AppendStringify(dst, usage.wire())
}

// usageWire is Usage's members in the order a provider that builds the usage as one literal creates them.
type usageWire struct {
	Input        int       `json:"input"`
	Output       int       `json:"output"`
	CacheRead    int       `json:"cacheRead"`
	CacheWrite   int       `json:"cacheWrite"`
	CacheWrite1h *int      `json:"cacheWrite1h,omitempty"`
	Reasoning    *int      `json:"reasoning,omitempty"`
	ImageOutput  *int      `json:"imageOutput,omitempty"`
	TotalTokens  int       `json:"totalTokens"`
	Cost         UsageCost `json:"cost"`
}

// usageWireSplitsAfterCost is Usage's members when the provider created cacheWrite1h and reasoning after cost.
type usageWireSplitsAfterCost struct {
	Input        int       `json:"input"`
	Output       int       `json:"output"`
	CacheRead    int       `json:"cacheRead"`
	CacheWrite   int       `json:"cacheWrite"`
	TotalTokens  int       `json:"totalTokens"`
	Cost         UsageCost `json:"cost"`
	CacheWrite1h *int      `json:"cacheWrite1h,omitempty"`
	Reasoning    *int      `json:"reasoning,omitempty"`
	ImageOutput  *int      `json:"imageOutput,omitempty"`
}

func (usage Usage) wire() usageWire {
	return usageWire{usage.Input, usage.Output, usage.CacheRead, usage.CacheWrite, usage.CacheWrite1h, usage.Reasoning, usage.ImageOutput, usage.TotalTokens, usage.Cost}
}

func (usage Usage) wireSplitsAfterCost() usageWireSplitsAfterCost {
	return usageWireSplitsAfterCost{usage.Input, usage.Output, usage.CacheRead, usage.CacheWrite, usage.TotalTokens, usage.Cost, usage.CacheWrite1h, usage.Reasoning, usage.ImageOutput}
}

// UnmarshalJSON decodes the upstream Usage wire shape and keeps where the source wrote cacheWrite1h, reasoning, and imageOutput.
func (usage *Usage) UnmarshalJSON(data []byte) error {
	if decoded, ok := parseUsageFast(data); ok {
		*usage = decoded
		return nil
	}
	type wire struct {
		Input        usageInt  `json:"input"`
		Output       usageInt  `json:"output"`
		CacheRead    usageInt  `json:"cacheRead"`
		CacheWrite   usageInt  `json:"cacheWrite"`
		CacheWrite1h *usageInt `json:"cacheWrite1h"`
		Reasoning    *usageInt `json:"reasoning"`
		ImageOutput  *usageInt `json:"imageOutput"`
		TotalTokens  usageInt  `json:"totalTokens"`
		Cost         UsageCost `json:"cost"`
	}
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*usage = Usage{Input: int(value.Input), Output: int(value.Output), CacheRead: int(value.CacheRead), CacheWrite: int(value.CacheWrite), CacheWrite1h: value.CacheWrite1h.ptr(),
		Reasoning: value.Reasoning.ptr(), ImageOutput: value.ImageOutput.ptr(), TotalTokens: int(value.TotalTokens), Cost: value.Cost}
	if value.CacheWrite1h != nil || value.Reasoning != nil || value.ImageOutput != nil {
		// Usage holds numbers only, so a member name occurs in the source only as a key.
		cost := bytes.Index(data, []byte(`"cost"`))
		usage.splitsAfterCost = cost >= 0 && (bytes.LastIndex(data, []byte(`"cacheWrite1h"`)) > cost || bytes.LastIndex(data, []byte(`"reasoning"`)) > cost || bytes.LastIndex(data, []byte(`"imageOutput"`)) > cost)
	}
	return nil
}

// parseUsageFast decodes standard canonical or whitespace-formatted Usage JSON objects
// without reflection or encoding/json/v2 struct-field cache allocations. Any non-standard
// form (escapes, unknown keys, null values, invalid JSON, scientific/fractional ints)
// returns (_, false) so UnmarshalJSON falls back to encoding/json.
func parseUsageFast(data []byte) (Usage, bool) {
	i := skipUsageSpace(data, 0)
	if i >= len(data) || data[i] != '{' {
		return Usage{}, false
	}
	i = skipUsageSpace(data, i+1)
	var u Usage
	var seenCost, splitsAfterCost bool
	if i < len(data) && data[i] == '}' {
		if skipUsageSpace(data, i+1) == len(data) {
			return u, true
		}
		return Usage{}, false
	}
	for {
		key, next, ok := scanUsageKey(data, i)
		if !ok {
			return Usage{}, false
		}
		i = skipUsageSpace(data, next)
		if i >= len(data) || data[i] != ':' {
			return Usage{}, false
		}
		i = skipUsageSpace(data, i+1)
		switch string(key) {
		case "input":
			v, n, ok := scanUsageInt(data, i)
			if !ok {
				return Usage{}, false
			}
			u.Input, i = v, n
		case "output":
			v, n, ok := scanUsageInt(data, i)
			if !ok {
				return Usage{}, false
			}
			u.Output, i = v, n
		case "cacheRead":
			v, n, ok := scanUsageInt(data, i)
			if !ok {
				return Usage{}, false
			}
			u.CacheRead, i = v, n
		case "cacheWrite":
			v, n, ok := scanUsageInt(data, i)
			if !ok {
				return Usage{}, false
			}
			u.CacheWrite, i = v, n
		case "cacheWrite1h":
			v, n, ok := scanUsageInt(data, i)
			if !ok {
				return Usage{}, false
			}
			u.CacheWrite1h, i = new(v), n
			if seenCost {
				splitsAfterCost = true
			}
		case "reasoning":
			v, n, ok := scanUsageInt(data, i)
			if !ok {
				return Usage{}, false
			}
			u.Reasoning, i = new(v), n
			if seenCost {
				splitsAfterCost = true
			}
		case "imageOutput":
			v, n, ok := scanUsageInt(data, i)
			if !ok {
				return Usage{}, false
			}
			u.ImageOutput, i = new(v), n
			if seenCost {
				splitsAfterCost = true
			}
		case "totalTokens":
			v, n, ok := scanUsageInt(data, i)
			if !ok {
				return Usage{}, false
			}
			u.TotalTokens, i = v, n
		case "cost":
			c, n, ok := scanUsageCost(data, i)
			if !ok {
				return Usage{}, false
			}
			u.Cost, i = c, n
			seenCost = true
		default:
			return Usage{}, false
		}
		i = skipUsageSpace(data, i)
		if i >= len(data) {
			return Usage{}, false
		}
		if data[i] == ',' {
			i = skipUsageSpace(data, i+1)
			continue
		}
		if data[i] == '}' {
			if skipUsageSpace(data, i+1) != len(data) {
				return Usage{}, false
			}
			u.splitsAfterCost = splitsAfterCost
			return u, true
		}
		return Usage{}, false
	}
}

func scanUsageCost(data []byte, i int) (UsageCost, int, bool) {
	if i >= len(data) || data[i] != '{' {
		return UsageCost{}, 0, false
	}
	i = skipUsageSpace(data, i+1)
	var c UsageCost
	if i < len(data) && data[i] == '}' {
		return c, i + 1, true
	}
	for {
		key, next, ok := scanUsageKey(data, i)
		if !ok {
			return UsageCost{}, 0, false
		}
		i = skipUsageSpace(data, next)
		if i >= len(data) || data[i] != ':' {
			return UsageCost{}, 0, false
		}
		i = skipUsageSpace(data, i+1)
		switch string(key) {
		case "input":
			v, n, ok := scanUsageFloat(data, i)
			if !ok {
				return UsageCost{}, 0, false
			}
			c.Input, i = v, n
		case "output":
			v, n, ok := scanUsageFloat(data, i)
			if !ok {
				return UsageCost{}, 0, false
			}
			c.Output, i = v, n
		case "cacheRead":
			v, n, ok := scanUsageFloat(data, i)
			if !ok {
				return UsageCost{}, 0, false
			}
			c.CacheRead, i = v, n
		case "cacheWrite":
			v, n, ok := scanUsageFloat(data, i)
			if !ok {
				return UsageCost{}, 0, false
			}
			c.CacheWrite, i = v, n
		case "total":
			v, n, ok := scanUsageFloat(data, i)
			if !ok {
				return UsageCost{}, 0, false
			}
			c.Total, i = v, n
		case "totalFromProvider":
			switch {
			case bytes.HasPrefix(data[i:], []byte("true")):
				c.TotalFromProvider = new(true)
				i += 4
			case bytes.HasPrefix(data[i:], []byte("false")):
				c.TotalFromProvider = new(false)
				i += 5
			default:
				return UsageCost{}, 0, false
			}
		default:
			return UsageCost{}, 0, false
		}
		i = skipUsageSpace(data, i)
		if i >= len(data) {
			return UsageCost{}, 0, false
		}
		if data[i] == ',' {
			i = skipUsageSpace(data, i+1)
			continue
		}
		if data[i] == '}' {
			return c, i + 1, true
		}
		return UsageCost{}, 0, false
	}
}

func skipUsageSpace(data []byte, i int) int {
	for i < len(data) {
		switch data[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

func scanUsageKey(data []byte, i int) ([]byte, int, bool) {
	if i >= len(data) || data[i] != '"' {
		return nil, 0, false
	}
	start := i + 1
	for j := start; j < len(data); j++ {
		c := data[j]
		if c == '"' {
			return data[start:j], j + 1, true
		}
		if c == '\\' || c < 0x20 {
			return nil, 0, false
		}
	}
	return nil, 0, false
}

func scanUsageInt(data []byte, i int) (int, int, bool) {
	if i >= len(data) {
		return 0, 0, false
	}
	neg := false
	if data[i] == '-' {
		neg = true
		i++
		if i >= len(data) {
			return 0, 0, false
		}
	}
	if data[i] < '0' || data[i] > '9' {
		return 0, 0, false
	}
	start := i
	var val uint64
	if data[i] == '0' {
		i++
		if i < len(data) && data[i] >= '0' && data[i] <= '9' {
			return 0, 0, false // leading zero invalid in JSON
		}
	} else {
		for i < len(data) && data[i] >= '0' && data[i] <= '9' {
			if i-start >= 18 {
				return 0, 0, false // let fallback handle clamping
			}
			val = val*10 + uint64(data[i]-'0')
			i++
		}
	}
	if i < len(data) && (data[i] == '.' || data[i] == 'e' || data[i] == 'E') {
		return 0, 0, false // let fallback handle fractional/scientific notation
	}
	if neg {
		return -int(val), i, true
	}
	return int(val), i, true
}

func scanUsageFloat(data []byte, i int) (float64, int, bool) {
	start := i
	if i >= len(data) {
		return 0, 0, false
	}
	if data[i] == '-' {
		i++
	}
	if i >= len(data) || data[i] < '0' || data[i] > '9' {
		return 0, 0, false
	}
	if data[i] == '0' {
		i++
	} else {
		for i < len(data) && data[i] >= '0' && data[i] <= '9' {
			i++
		}
	}
	if i < len(data) && data[i] == '.' {
		i++
		if i >= len(data) || data[i] < '0' || data[i] > '9' {
			return 0, 0, false
		}
		for i < len(data) && data[i] >= '0' && data[i] <= '9' {
			i++
		}
	}
	if i < len(data) && (data[i] == 'e' || data[i] == 'E') {
		i++
		if i < len(data) && (data[i] == '+' || data[i] == '-') {
			i++
		}
		if i >= len(data) || data[i] < '0' || data[i] > '9' {
			return 0, 0, false
		}
		for i < len(data) && data[i] >= '0' && data[i] <= '9' {
			i++
		}
	}
	// Check single zero fast path without ParseFloat
	if i == start+1 && data[start] == '0' {
		return 0, i, true
	}
	f, err := strconv.ParseFloat(string(data[start:i]), 64)
	if err != nil {
		return 0, 0, false
	}
	return f, i, true
}

// usageInt reads an integer member the way JSON.parse reads a number: any spelling of a whole number (1e2, 5.0) is that integer, a fraction
// truncates toward zero and a value outside int clamps, so a session message whose count was written that way is kept as Pi keeps it. Anything
// that is not a JSON number is an error.
type usageInt int

func (n *usageInt) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	if len(data) == 0 || data[0] == '"' || data[0] == 't' || data[0] == 'f' || data[0] == '[' || data[0] == '{' {
		return fmt.Errorf("cannot unmarshal %s into an integer", data)
	}
	f, err := strconv.ParseFloat(string(data), 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return fmt.Errorf("cannot unmarshal %s into an integer: %w", data, err)
	}
	switch {
	case math.IsNaN(f):
		return fmt.Errorf("cannot unmarshal %s into an integer", data)
	case f >= math.MaxInt:
		*n = math.MaxInt
	case f <= math.MinInt:
		*n = math.MinInt
	default:
		*n = usageInt(f)
	}
	return nil
}

func (n *usageInt) ptr() *int {
	if n == nil {
		return nil
	}
	value := int(*n)
	return &value
}
