package testfixture

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The tool "refine-probe" (TestConformance_RefinedToolArgumentsAgreeAcrossSDKs): a refined string, an optional refined integer, a union whose branches are refined strings, an optional
// string that throws, and a refined root, the schema of the "probe" cases of test/parity/sdk-vectors/pi-ai/validateToolArguments.json. Every predicate and message function logs its call
// ("check:len", "error:lenmsg") so the test can compare the order of the calls with Pi's. The command refine_log reports and clears the log.
var (
	refineLogMu sync.Mutex
	refineLog   []string
)

func refineRecord(kind, tag string) {
	refineLogMu.Lock()
	defer refineLogMu.Unlock()
	refineLog = append(refineLog, kind+":"+tag)
}

func refineCheck(tag string, check func(any) (bool, error)) func(any) (bool, error) {
	return func(value any) (bool, error) {
		refineRecord("check", tag)
		return check(value)
	}
}

func refineText(tag string, message func(any) string) func(any) (string, error) {
	return func(value any) (string, error) {
		refineRecord("error", tag)
		return message(value), nil
	}
}

func registerRefineProbe(ext *sdk.Extension) {
	typ := sdk.Type
	name := typ.Refine(typ.String(), refineCheck("len", func(v any) (bool, error) {
		text, _ := v.(string)
		return len(text) >= 3, nil
	}), refineText("lenmsg", func(any) string { return "name is too short" }))
	qty := typ.Optional(typ.Refine(typ.Integer(), refineCheck("even", func(v any) (bool, error) {
		number, _ := v.(float64)
		return math.Mod(number, 2) == 0, nil
	}), refineText("evenmsg", func(v any) string {
		number, _ := v.(float64)
		return "qty must be even, got " + strconv.FormatFloat(number, 'f', -1, 64)
	})))
	equals := func(tag, want string) *sdk.TSchema {
		return typ.Refine(typ.String(), refineCheck(tag, func(v any) (bool, error) { return v == want, nil }), refineText(tag+"m", func(any) string { return "not " + want }))
	}
	pick := typ.Union([]any{equals("u1", "a"), equals("u2", "b")})
	boom := typ.Optional(typ.Refine(typ.String(), refineCheck("boom", func(any) (bool, error) { return false, errors.New("kaboom") })))
	root := typ.Refine(typ.Object(sdk.NewOrderedObject("name", name, "qty", qty, "pick", pick, "boom", boom)), refineCheck("root", func(any) (bool, error) { return true, nil }), refineText("rootmsg", func(any) string { return "root refused" }))
	ext.Tool("refine-probe", "Arguments checked by refinements", root.Schema(), func(sdk.Context, map[string]any) (any, error) { return "ok", nil })
	ext.Command("refine_log", "Report and clear the refinement call log", func(ctx sdk.Context, _ string) error {
		refineLogMu.Lock()
		calls := append([]string{}, refineLog...)
		refineLog = nil
		refineLogMu.Unlock()
		wire, err := json.Marshal(calls)
		if err != nil {
			return err
		}
		ctx.Notify("refine_log:"+string(wire)+":end", "info")
		return nil
	})
}
