package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// MarshalJSON emits the upstream Usage wire shape. A provider creates cacheWrite1h and reasoning on a usage object that already holds
// totalTokens and cost (anthropic-messages.ts:684, :846-851, bedrock-converse-stream.ts:715), so JSON.stringify writes them last; usage
// built as one literal (usage-totals.ts combineUsage) holds them before totalTokens.
func (usage Usage) MarshalJSON() ([]byte, error) {
	if usage.splitsAfterCost {
		return json.Marshal(usage.wireSplitsAfterCost())
	}
	return json.Marshal(usage.wire())
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
}

func (usage Usage) wire() usageWire {
	return usageWire{usage.Input, usage.Output, usage.CacheRead, usage.CacheWrite, usage.CacheWrite1h, usage.Reasoning, usage.TotalTokens, usage.Cost}
}

func (usage Usage) wireSplitsAfterCost() usageWireSplitsAfterCost {
	return usageWireSplitsAfterCost{usage.Input, usage.Output, usage.CacheRead, usage.CacheWrite, usage.TotalTokens, usage.Cost, usage.CacheWrite1h, usage.Reasoning}
}

// UnmarshalJSON decodes the upstream Usage wire shape and keeps where the source wrote cacheWrite1h and reasoning.
func (usage *Usage) UnmarshalJSON(data []byte) error {
	type wire struct {
		Input        usageInt  `json:"input"`
		Output       usageInt  `json:"output"`
		CacheRead    usageInt  `json:"cacheRead"`
		CacheWrite   usageInt  `json:"cacheWrite"`
		CacheWrite1h *usageInt `json:"cacheWrite1h"`
		Reasoning    *usageInt `json:"reasoning"`
		TotalTokens  usageInt  `json:"totalTokens"`
		Cost         UsageCost `json:"cost"`
	}
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*usage = Usage{Input: int(value.Input), Output: int(value.Output), CacheRead: int(value.CacheRead), CacheWrite: int(value.CacheWrite), CacheWrite1h: value.CacheWrite1h.ptr(),
		Reasoning: value.Reasoning.ptr(), TotalTokens: int(value.TotalTokens), Cost: value.Cost}
	if value.CacheWrite1h != nil || value.Reasoning != nil {
		// Usage holds numbers only, so a member name occurs in the source only as a key.
		cost := bytes.Index(data, []byte(`"cost"`))
		usage.splitsAfterCost = cost >= 0 && (bytes.LastIndex(data, []byte(`"cacheWrite1h"`)) > cost || bytes.LastIndex(data, []byte(`"reasoning"`)) > cost)
	}
	return nil
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
