package jsstring

import (
	"bytes"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// referenceStringifyLength is the length computed from a separately allocated MarshalJSON string, the way ai.JSONStringifyLength once did it.
func referenceStringifyLength(value any) (int, error) {
	encoded, err := referenceMarshalJSON(value)
	if err != nil {
		return 0, err
	}
	text := string(encoded)
	length := Length(text)
	for i := 0; i < len(text); i++ {
		if text[i] != '\\' {
			continue
		}
		if strings.HasPrefix(text[i:], `\ufffd`) {
			length -= len(`\ufffd`) - 1
			i += len(`\ufffd`) - 1
			continue
		}
		i++
	}
	return length, nil
}

func randomStringifyValue(random *rand.Rand, depth int) any {
	pieces := []string{"a", "é", "\u2028", "\u2029", "<", ">", "&", "\"", "\\", "\n", "\t", "😀", "\xff", "\xed\xa0\x80", "\\ufffd", "\ufffd", "-0", "\x00", "\x7f"}
	switch kind := random.Intn(9); {
	case kind < 3 || depth > 3:
		var text strings.Builder
		for range random.Intn(6) {
			text.WriteString(pieces[random.Intn(len(pieces))])
		}
		return text.String()
	case kind == 3:
		return []float64{math.Copysign(0, -1), 1.5, 1e21, math.NaN(), math.Inf(1), -2}[random.Intn(6)]
	case kind == 4:
		return random.Intn(100) - 50
	case kind == 5:
		return nil
	case kind == 6:
		return random.Intn(2) == 0
	case kind == 7:
		items := make([]any, random.Intn(4))
		for i := range items {
			items[i] = randomStringifyValue(random, depth+1)
		}
		return items
	default:
		members := map[string]any{}
		for range random.Intn(4) {
			members[randomStringifyValue(random, 9).(string)] = randomStringifyValue(random, depth+1)
		}
		return members
	}
}

func TestStringifyLengthMatchesTheLengthOfTheStringifiedText(t *testing.T) {
	for seed := range int64(5000) {
		random := rand.New(rand.NewSource(seed))
		value := randomStringifyValue(random, 0)
		want, wantErr := referenceStringifyLength(value)
		got, gotErr := StringifyLength(value)
		if (wantErr == nil) != (gotErr == nil) || got != want {
			t.Fatalf("seed %d %#v: got %d, %v; want %d, %v", seed, value, got, gotErr, want, wantErr)
		}
	}
	if _, err := StringifyLength(make(chan int)); err == nil {
		t.Fatal("an unencodable value must fail")
	}
}

// MarshalJSON's result belongs to the caller: the pooled buffer behind it is reused by the next call, concurrently too.
func TestMarshalJSONResultsAreIndependentOfThePool(t *testing.T) {
	var wait sync.WaitGroup
	for worker := range 8 {
		wait.Go(func() {
			random := rand.New(rand.NewSource(int64(worker)))
			for range 300 {
				value := randomStringifyValue(random, 0)
				first, err := MarshalJSON(value)
				if err != nil {
					continue
				}
				kept := string(first)
				_, _ = StringifyLength(randomStringifyValue(random, 0))
				_, _ = MarshalJSON(randomStringifyValue(random, 0))
				if string(first) != kept {
					t.Errorf("worker %d: a later call rewrote an earlier result", worker)
					return
				}
				if again, _ := MarshalJSON(value); string(again) != kept {
					t.Errorf("worker %d: %q then %q", worker, kept, again)
					return
				}
			}
		})
	}
	wait.Wait()
}

// referenceMarshalJSON is MarshalJSON as it was written before the encoder and its buffer were pooled: a fresh buffer and a separate output slice.
// Since LW-JSON-1 the rewrite also writes the \ufffd escape (any hex case) raw, as JSON.stringify writes U+FFFD.
func referenceMarshalJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetJSNumbers(true)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	input := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	output := make([]byte, 0, len(input))
	inString := false
	for i := 0; i < len(input); i++ {
		c := input[i]
		if !inString {
			switch c {
			case '"':
				inString = true
			case '-':
				// A custom marshaler may emit negative zero itself ("-0" from a float64 -0, or a zero literal kept as text); JSON.stringify writes 0.
				end := i + 1
				for end < len(input) && isJSONNumberByte(input[end]) {
					end++
				}
				if value, err := strconv.ParseFloat(string(input[i:end]), 64); value == 0 && err == nil {
					output = append(output, '0')
					i = end - 1
					continue
				}
			}
			output = append(output, c)
			continue
		}
		if c == '"' {
			inString = false
			output = append(output, c)
			continue
		}
		if c == '\\' && i+1 < len(input) {
			if i+6 <= len(input) {
				var replacement string
				switch string(input[i : i+6]) {
				case `\u003c`, `\u003C`:
					replacement = "<"
				case `\u003e`, `\u003E`:
					replacement = ">"
				case `\u0026`:
					replacement = "&"
				case `\u2028`:
					replacement = "\u2028"
				case `\u2029`:
					replacement = "\u2029"
				}
				if strings.EqualFold(string(input[i:i+6]), `\ufffd`) && input[i+1] == 'u' {
					replacement = "\ufffd"
				}
				if replacement != "" {
					output = append(output, replacement...)
					i += 5
					continue
				}
			}
			output = append(output, input[i], input[i+1])
			i++
			continue
		}
		output = append(output, c)
	}
	return output, nil
}

func isJSONNumberByte(c byte) bool {
	return c >= '0' && c <= '9' || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-'
}

func TestMarshalJSONMatchesTheUnpooledEncoding(t *testing.T) {
	for seed := range int64(5000) {
		random := rand.New(rand.NewSource(seed))
		value := randomStringifyValue(random, 0)
		want, wantErr := referenceMarshalJSON(value)
		got, gotErr := MarshalJSON(value)
		if (wantErr == nil) != (gotErr == nil) || !bytes.Equal(got, want) {
			t.Fatalf("seed %d %#v: got %q, %v; want %q, %v", seed, value, got, gotErr, want, wantErr)
		}
	}
}
