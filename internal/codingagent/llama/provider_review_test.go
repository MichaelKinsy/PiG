package llama

import (
	"math"
	"reflect"
	"testing"
)

// largestSafeWindow is Number.MAX_SAFE_INTEGER where Go's int holds it; a 32-bit int cannot, so the window is unconfigured there.
var largestSafeWindow = func() int {
	if uint64(math.MaxInt) >= 1<<53-1 {
		return int(uint64(1<<53 - 1))
	}
	return 0
}()

// serverArgNumber bounds a base-prefixed literal on its uint64 value: past Number.MAX_SAFE_INTEGER it reports no value, which configuredContextWindow's Number.isSafeInteger check (provider.ts) would reject anyway, so the int conversion downstream never sees an out-of-range value (CodeQL go/incorrect-integer-conversion).
func TestServerArgNumberBoundsBasePrefixedLiterals(t *testing.T) {
	for _, tc := range []struct {
		text string
		want float64
		ok   bool
	}{
		{"0x1000", 4096, true},
		{"0b101", 5, true},
		{"0o17", 15, true},
		{"0x20000000000001", 0, false},
		{"0b100000000000000000000000000000000000000000000000000000", 0, false},
		{"0xFFFFFFFFFFFFFFFF", 0, false},
		{"0x10000000000000000", 0, false},
		{"0xg", 0, false},
	} {
		if got, ok := serverArgNumber(tc.text); got != tc.want || ok != tc.ok {
			t.Errorf("serverArgNumber(%q) = %v, %v, want %v, %v", tc.text, got, ok, tc.want, tc.ok)
		}
	}
	if got, ok := serverArgNumber("0x1FFFFFFFFFFFFF"); ok != (largestSafeWindow != 0) || (ok && got != 1<<53-1) {
		t.Errorf("serverArgNumber(Number.MAX_SAFE_INTEGER as hex) = %v, %v", got, ok)
	}
}

// provider.ts configuredContextWindow: the first --ctx-size, -c or -ctx whose next argument is Number()-parsed to a positive safe integer; a flag that ends the list has no value.
func TestConfiguredContextWindowReadsEveryContextFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"--ctx-size", []string{"llama-server", "--ctx-size", "32768"}, 32768},
		{"-c", []string{"llama-server", "-c", "8192"}, 8192},
		{"-ctx", []string{"llama-server", "-ctx", "16384"}, 16384},
		{"surrounding whitespace", []string{"-c", " 4096\n"}, 4096},
		{"hex literal", []string{"-c", "0x1000"}, 4096},
		{"exponent", []string{"-c", "1e4"}, 10000},
		{"fraction", []string{"-c", "4096.5"}, 0},
		{"negative", []string{"-c", "-1"}, 0},
		{"empty", []string{"-c", ""}, 0},
		{"not a number", []string{"-c", "big"}, 0},
		{"unsafe integer", []string{"-c", "9007199254740993"}, 0},
		{"signed hex", []string{"-c", "-0x10"}, 0},
		{"largest safe hex", []string{"-c", "0x1FFFFFFFFFFFFF"}, largestSafeWindow},
		{"hex past the safe range", []string{"-c", "0x20000000000001"}, 0},
		{"octal past the safe range", []string{"-c", "0o400000000000000001"}, 0},
		{"hex past uint64", []string{"-c", "0x10000000000000000"}, 0},
		{"first valid flag wins", []string{"-c", "big", "--ctx-size", "2048", "-ctx", "1024"}, 2048},
		{"flag without value", []string{"llama-server", "--ctx-size"}, 0},
		{"other flag", []string{"--ctx", "4096"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := configuredContextWindow(LlamaModelInfo{Status: LlamaModelInfoStatus{Args: tc.args}}); got != tc.want {
				t.Fatalf("configuredContextWindow(%q) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

// provider.ts getAllModels: [...models, ...classifiers].
func TestGetAllModelsListsChatModelsBeforeClassifiers(t *testing.T) {
	controller := CreateLlamaProvider()
	controller.SetCatalog([]LlamaModelInfo{
		{ID: "a", Status: LlamaModelInfoStatus{Value: LlamaModelStatusLoaded}},
		{ID: "b", Status: LlamaModelInfoStatus{Value: LlamaModelStatusLoaded}},
	}, "http://127.0.0.1:8080", false)
	var got []string
	for _, model := range controller.Provider.GetAllModels() {
		switch typed := model.(type) {
		case Model:
			got = append(got, "chat:"+typed.ID)
		case ClassifierModel:
			got = append(got, "classifier:"+typed.ID)
		}
	}
	if want := []string{"chat:a", "chat:b", "classifier:a", "classifier:b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GetAllModels = %v, want %v", got, want)
	}
}

// provider.ts configuredContextWindow keeps a positive Number.isSafeInteger value. Go's int is narrower than a safe integer on 32-bit platforms, so the conversion is bounded by the int range as well: an out-of-range window is unconfigured, never a wrapped value (CodeQL go/incorrect-integer-conversion).
func TestPositiveSafeIntStaysWithinTheIntRange(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value float64
		limit int
		want  int
		ok    bool
	}{
		{"within a 32-bit int", math.MaxInt32, math.MaxInt32, math.MaxInt32, true},
		{"one past a 32-bit int", math.MaxInt32 + 1, math.MaxInt32, 0, false},
		{"a hex literal past a 32-bit int", 0x100000001, math.MaxInt32, 0, false},
		{"within a 64-bit int", 4294967297, math.MaxInt, 4294967297, true},
		{"largest safe integer", 1<<53 - 1, math.MaxInt, 1<<53 - 1, true},
		{"first unsafe integer", 1 << 53, math.MaxInt, 0, false},
		{"zero", 0, math.MaxInt, 0, false},
		{"negative", -1, math.MaxInt, 0, false},
		{"fraction", 4096.5, math.MaxInt, 0, false},
		{"infinity", math.Inf(1), math.MaxInt, 0, false},
		{"not a number", math.NaN(), math.MaxInt, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := positiveSafeInt(tc.value, tc.limit); got != tc.want || ok != tc.ok {
				t.Fatalf("positiveSafeInt(%v, %d) = %d, %v, want %d, %v", tc.value, tc.limit, got, ok, tc.want, tc.ok)
			}
		})
	}
}
