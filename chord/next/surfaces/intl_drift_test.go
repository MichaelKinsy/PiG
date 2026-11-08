package surfaces

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The drift test: the generated tables (intl_gen.go) against the Node that generated them. It runs Node over a large
// random corpus (locales including unavailable, aliased and malformed tags; every option shape; values from rounding
// halves to the double range; every minute of a day) and compares byte for byte, errors included. `make intl-tables`
// regenerates the tables after a pin bump; INTL_DRIFT_CASES raises the corpus size and INTL_DRIFT_SEED replays a seed.

const driftScript = `
const lines = require("fs").readFileSync(0, "utf8").split("\n").filter(Boolean);
const out = [];
for (const line of lines) {
  const c = JSON.parse(line);
  try {
    if (c.kind === "number") out.push({ s: new Intl.NumberFormat(c.locale === null ? undefined : c.locale, c.options).format(c.value === "NaN" ? NaN : c.value === "-0" ? -0 : c.value === "Infinity" ? Infinity : c.value === "-Infinity" ? -Infinity : c.value) });
    else out.push({ s: new Intl.DateTimeFormat(c.locale === null ? undefined : c.locale, c.options).format(new Date(c.ms)) });
  } catch (e) {
    out.push({ err: e.name });
  }
}
process.stdout.write(JSON.stringify(out));
`

type driftCase struct {
	Kind    string         `json:"kind"`
	Locale  *string        `json:"locale"`
	Options map[string]any `json:"options,omitempty"`
	Value   any            `json:"value,omitempty"`
	MS      int64          `json:"ms,omitempty"`

	numberOptions *NumberOptions
	timeOptions   *TimeOptions
	number        float64
	at            time.Time
}

type driftResult struct {
	S   string `json:"s"`
	Err string `json:"err"`
}

func driftNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed: the tables are not checked against Intl")
	}
	out, err := exec.Command(node, "-p", "process.versions.icu").Output()
	if err != nil {
		t.Fatalf("node -p: %v", err)
	}
	if icu := strings.TrimSpace(string(out)); icu != intlOracleICU {
		t.Skipf("node has ICU %s, the tables are from ICU %s: run this test with the pinned Node (make intl-tables after a pin bump)", icu, intlOracleICU)
	}
	return node
}

func runNode(t *testing.T, node string, cases []driftCase) []driftResult {
	t.Helper()
	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	for i := range cases {
		if err := enc.Encode(&cases[i]); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(node, "-e", driftScript)
	cmd.Stdin = &in
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var results []driftResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != len(cases) {
		t.Fatalf("node answered %d of %d cases", len(results), len(cases))
	}
	return results
}

func driftSeed(t *testing.T) *rand.Rand {
	seed := uint64(time.Now().UnixNano())
	if s := os.Getenv("INTL_DRIFT_SEED"); s != "" {
		seed, _ = strconv.ParseUint(s, 10, 64)
	}
	t.Logf("INTL_DRIFT_SEED=%d", seed)
	return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
}

func driftCases(t *testing.T) int {
	n := 20000
	if s := os.Getenv("INTL_DRIFT_CASES"); s != "" {
		n, _ = strconv.Atoi(s)
	}
	if testing.Short() {
		n = 2000
	}
	return n
}

var (
	driftScripts = []string{"Latn", "Cyrl", "Arab", "Hans", "Hant", "Deva", "Beng", "Guru", "Qaai", "Zzzz"}
	driftRegions = []string{"US", "GB", "DE", "CH", "FR", "CA", "IN", "EG", "SA", "BR", "PT", "ES", "MX", "419", "001", "UK", "SU", "172", "810", "ZZ", "TW", "HK", "CN", "JP", "RU", "BY", "AZ", "IR", "AF", "ME", "RS", "XK", "AN", "DD", "BU"}
	driftLangs   = []string{"en", "de", "fr", "es", "pt", "zh", "ar", "fa", "he", "iw", "in", "ja", "ko", "ru", "uk", "sr", "sh", "mo", "no", "nb", "tl", "fil", "bn", "my", "hi", "ne", "ur", "th", "fi", "az", "bs", "ccp", "mzn", "ps", "lag", "za", "yue", "cmn", "und", "xx", "eng", "deu", "zzz", "root"}
	driftOdd     = []string{"en-US-posix", "ca-ES-valencia", "de-1996", "en-GB-oxendict", "en-x-private", "en-t-hi", "en-a-bbb", "EN-us", "ZH-hANT-tw", "en-US-US", "en-1996-1996", "de-DE-u"}
)

func driftLocale(r *rand.Rand) string {
	switch r.IntN(10) {
	case 0:
		return driftOdd[r.IntN(len(driftOdd))]
	case 1, 2:
		list := intlNumberLocales[:]
		if r.IntN(2) == 0 {
			list = intlTimeLocales[:]
		}
		return list[r.IntN(len(list))]
	}
	tag := driftLangs[r.IntN(len(driftLangs))]
	if r.IntN(3) == 0 {
		tag += "-" + driftScripts[r.IntN(len(driftScripts))]
	}
	if r.IntN(2) == 0 {
		tag += "-" + driftRegions[r.IntN(len(driftRegions))]
	}
	if r.IntN(8) == 0 {
		tag = strings.ToUpper(tag[:1]) + strings.ToLower(tag[1:])
	}
	return tag
}

func driftValue(r *rand.Rand) float64 {
	switch r.IntN(12) {
	case 0:
		return math.Float64frombits(r.Uint64())
	case 1:
		return float64(r.IntN(2_000_000)-1_000_000) / 8 // exact binary halves and quarters
	case 2:
		return (float64(r.IntN(2_000_000_000)) + 0.5) / math.Pow10(r.IntN(8)) // decimal halves
	case 3:
		return float64(r.Int64N(1<<53)) * math.Pow10(r.IntN(10)-3)
	case 4:
		return []float64{0, math.Copysign(0, -1), math.NaN(), math.Inf(1), math.Inf(-1), 5e-324, math.MaxFloat64, 1e21, 1e-7, 999.9995, 0.0005, 1.0005}[r.IntN(12)]
	case 5:
		return r.NormFloat64() * math.Pow10(r.IntN(30)-10)
	case 6:
		return math.Round(r.Float64()*1e6) / 1e3
	}
	return float64(r.IntN(100000000)) / math.Pow10(r.IntN(6))
}

func TestNumbersMatchNode(t *testing.T) {
	node := driftNode(t)
	r := driftSeed(t)
	t.Setenv("LC_ALL", "en_US.UTF-8")
	n := driftCases(t)
	cases := make([]driftCase, 0, n)
	for range n {
		c := driftCase{Kind: "number", number: driftValue(r)}
		locale := driftLocale(r)
		if r.IntN(25) == 0 {
			c.Locale = nil
		} else {
			c.Locale = &locale
		}
		switch {
		case math.IsNaN(c.number):
			c.Value = "NaN"
		case math.IsInf(c.number, 1):
			c.Value = "Infinity"
		case math.IsInf(c.number, -1):
			c.Value = "-Infinity"
		case c.number == 0 && math.Signbit(c.number):
			c.Value = "-0"
		default:
			c.Value = c.number
		}
		if r.IntN(2) == 0 {
			c.Options = map[string]any{}
			c.numberOptions = &NumberOptions{}
			pick := func() int {
				if r.IntN(10) == 0 {
					return r.IntN(104) - 2 // out of range now and then
				}
				return r.IntN(9)
			}
			if r.IntN(2) == 0 {
				v := pick()
				c.Options["minimumFractionDigits"], c.numberOptions.MinimumFractionDigits = v, &v
			}
			if r.IntN(2) == 0 {
				v := pick()
				c.Options["maximumFractionDigits"], c.numberOptions.MaximumFractionDigits = v, &v
			}
		}
		cases = append(cases, c)
	}
	want := runNode(t, node, cases)
	bad := 0
	for i, c := range cases {
		var lang *Language
		if c.Locale != nil {
			lang = &Language{Locale: *c.Locale}
		}
		got, err := FormatNumber(lang, c.number, c.numberOptions)
		if !sameAsNode(got, err, want[i]) && bad < 30 {
			bad++
			t.Errorf("number %s %v %v: go %q (%v), node %+v", driftTag(c.Locale), c.Value, c.Options, got, err, want[i])
		}
	}
}

func driftTag(l *string) string {
	if l == nil {
		return "<default>"
	}
	return *l
}

func sameAsNode(got string, err error, want driftResult) bool {
	if want.Err != "" {
		return err != nil
	}
	return err == nil && got == want.S
}

func TestTimesMatchNode(t *testing.T) {
	node := driftNode(t)
	r := driftSeed(t)
	t.Setenv("LC_ALL", "en_US.UTF-8")
	oldLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = oldLocal })
	styles := []TimeStyle{"", TimeStyleShort, TimeStyleMedium}
	fields := []FieldStyle{"", FieldNumeric, FieldTwoDigit}
	n := driftCases(t)
	cases := make([]driftCase, 0, n)
	for range n {
		c := driftCase{Kind: "time"}
		locale := driftLocale(r)
		if r.IntN(25) == 0 {
			c.Locale = nil
		} else {
			c.Locale = &locale
		}
		c.MS = r.Int64N(4_000_000_000_000) // 1970 to 2096
		c.at = time.UnixMilli(c.MS)
		c.Options = map[string]any{"timeZone": "UTC"}
		o := TimeOptions{TimeStyle: styles[r.IntN(3)], Hour: fields[r.IntN(3)], Minute: fields[r.IntN(3)], Second: fields[r.IntN(3)]}
		if r.IntN(3) != 0 {
			b := r.IntN(2) == 0
			o.Hour12 = &b
			c.Options["hour12"] = b
		}
		if o.TimeStyle != "" {
			o.Hour, o.Minute, o.Second = "", "", "" // Intl throws a TypeError for both; D464 covers that in its own test
		} else if o.Hour == "" && o.Minute == "" && o.Second == "" {
			o.TimeStyle = TimeStyleShort // none would print a date
		}
		for k, v := range map[string]string{"timeStyle": string(o.TimeStyle), "hour": string(o.Hour), "minute": string(o.Minute), "second": string(o.Second)} {
			if v != "" {
				c.Options[k] = v
			}
		}
		c.timeOptions = &o
		cases = append(cases, c)
	}
	// every minute of a day in a few locales and shapes
	for _, locale := range []string{"en-US", "de", "ja", "ar-EG", "fa", "hi", "zh-TW", "fr-CA", "bn", "my"} {
		for _, shape := range []TimeOptions{{TimeStyle: TimeStyleShort}, {Hour: FieldNumeric}, {Hour: FieldTwoDigit, Minute: FieldTwoDigit, Second: FieldNumeric}, {TimeStyle: TimeStyleMedium}} {
			for minute := 0; minute < 24*60; minute++ {
				ms := time.Date(2026, 6, 15, minute/60, minute%60, minute%60, 0, time.UTC).UnixMilli()
				o := shape
				c := driftCase{Kind: "time", Locale: &locale, MS: ms, at: time.UnixMilli(ms), timeOptions: &o, Options: map[string]any{"timeZone": "UTC"}}
				for k, v := range map[string]string{"timeStyle": string(o.TimeStyle), "hour": string(o.Hour), "minute": string(o.Minute), "second": string(o.Second)} {
					if v != "" {
						c.Options[k] = v
					}
				}
				cases = append(cases, c)
			}
		}
	}
	want := runNode(t, node, cases)
	bad := 0
	for i, c := range cases {
		var lang *Language
		if c.Locale != nil {
			lang = &Language{Locale: *c.Locale}
		}
		got, err := FormatTime(lang, c.at, c.timeOptions)
		if !sameAsNode(got, err, want[i]) && bad < 30 {
			bad++
			t.Errorf("time %s %d %v: go %q (%v), node %+v", driftTag(c.Locale), c.MS, c.Options, got, err, want[i])
		}
	}
}

// The slots the tables carry are the valid option shapes of TimeOptions.
func TestTimeShapeSlotsAreTheValidShapes(t *testing.T) {
	var want []uint8
	styles := []TimeStyle{"", TimeStyleShort, TimeStyleMedium}
	fields := []FieldStyle{"", FieldNumeric, FieldTwoDigit}
	yes, no := true, false
	h12 := []*bool{nil, &yes, &no}
	for _, st := range styles {
		for _, h := range fields {
			for _, m := range fields {
				for _, s := range fields {
					for _, t12 := range h12 {
						if slot, err := (TimeOptions{st, h, m, s, t12}).slot(); err == nil {
							want = append(want, uint8(slot))
						}
					}
				}
			}
		}
	}
	if !slices.Equal(want, intlTimeShapeSlots[:]) {
		t.Errorf("slots = %v, tables have %v", want, intlTimeShapeSlots)
	}
}

// Every table is sorted for its binary search, and every row refers to a pattern.
func TestIntlTablesAreWellFormed(t *testing.T) {
	for name, list := range map[string][]string{"number": intlNumberLocales[:], "time": intlTimeLocales[:]} {
		if !slices.IsSorted(list) {
			t.Errorf("%s locales are not sorted", name)
		}
	}
	for name, pairs := range map[string][]localePair{"language": intlLanguageAliases[:], "script": intlScriptAliases[:], "region": intlRegionAliases[:], "number exceptions": intlNumberExceptions[:], "time exceptions": intlTimeExceptions[:]} {
		if !slices.IsSortedFunc(pairs, func(a, b localePair) int { return strings.Compare(a.from, b.from) }) {
			t.Errorf("%s pairs are not sorted", name)
		}
	}
	for _, to := range intlNumberExceptions {
		if _, found := slices.BinarySearch(intlNumberLocales[:], to.to); !found {
			t.Errorf("number exception %v targets an unavailable locale", to)
		}
	}
	for _, to := range intlTimeExceptions {
		if _, found := slices.BinarySearch(intlTimeLocales[:], to.to); !found {
			t.Errorf("time exception %v targets an unavailable locale", to)
		}
	}
	for i, l := range intlTimeLocaleData {
		if int(l.row) >= len(intlTimeRows) {
			t.Errorf("locale %s: row %d", intlTimeLocales[i], l.row)
		}
	}
	for _, row := range intlTimeRows {
		for _, p := range row {
			if int(p) >= len(intlTimePatterns) {
				t.Fatalf("pattern %d of %d", p, len(intlTimePatterns))
			}
		}
	}
}
