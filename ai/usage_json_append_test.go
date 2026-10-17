package ai

import (
	"bytes"
	"math"
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Usage appends the text the line encoder writes for its MarshalJSON output, in both member orders, and fails for a non-finite cost with MarshalJSON's error.
func TestUsageAppendStringifyMatchesMarshalJSON(t *testing.T) {
	costs := []float64{0, math.Copysign(0, -1), 0.000003, 1.5, 1e21, 1e-7, math.MaxFloat64, 0.1 + 0.2}
	for i, cost := range costs {
		for _, splits := range []bool{false, true} {
			usage := Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, TotalTokens: 10, Cost: UsageCost{Input: cost, Output: costs[(i+1)%len(costs)], CacheRead: cost, CacheWrite: -cost, Total: cost},
				CacheWrite1h: new(7), Reasoning: new(8), splitsAfterCost: splits}
			encoded, err := usage.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			want, err := sdkjson.MarshalStringify(rawText(encoded))
			if err != nil {
				t.Fatal(err)
			}
			got, err := usage.AppendStringify([]byte("prefix"))
			if err != nil || !bytes.Equal(got, append([]byte("prefix"), want...)) {
				t.Fatalf("cost %v splits %v:\n got %q (%v)\nwant %q", cost, splits, got, err, want)
			}
		}
	}
	for _, cost := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		usage := Usage{Cost: UsageCost{CacheRead: cost}}
		_, wantErr := usage.MarshalJSON()
		got, err := usage.AppendStringify([]byte("prefix"))
		if wantErr == nil || err == nil || err.Error() != wantErr.Error() || string(got) != "prefix" {
			t.Fatalf("cost %v: AppendStringify = %q, %v; MarshalJSON error %v", cost, got, err, wantErr)
		}
	}
}

// rawText is a Marshaler that returns its text, which the line encoder compacts and rewrites.
type rawText []byte

func (text rawText) MarshalJSON() ([]byte, error) { return text, nil }
