package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// The vectors of pi-ai's validateToolArguments for tools whose parameters carry TypeBox refinements (test/parity/sdk-vectors/pi-ai/validateToolArguments.json): what Pi returned or threw
// for each tool call, and the order of every predicate and message call TypeBox made on the way. The host's traversal (validate_refine.go) must make the same calls in the same order.

type oracleSpec struct {
	Fn      string          `json:"$fn"`
	Tag     string          `json:"tag"`
	N       float64         `json:"n"`
	Value   json.RawMessage `json:"value"`
	Text    string          `json:"text"`
	Prefix  string          `json:"prefix"`
	Message string          `json:"message"`
}

type oracleSite struct {
	Path        string `json:"path"`
	Refinements []struct {
		Check oracleSpec  `json:"check"`
		Error *oracleSpec `json:"error"`
	} `json:"refinements"`
}

type oracleRefiner struct {
	sites []oracleSite
	calls *[]string
}

func (o oracleRefiner) RefinementSites() []RefinementSite {
	out := make([]RefinementSite, len(o.sites))
	for i, site := range o.sites {
		out[i] = RefinementSite{Path: site.Path, Count: len(site.Refinements)}
	}
	return out
}

func (spec oracleSpec) record(kind string, calls *[]string) {
	tag := spec.Tag
	if tag == "" {
		tag = spec.Fn
	}
	*calls = append(*calls, kind+":"+tag)
}

func (o oracleRefiner) Refine(_ context.Context, path string, index int, value any, withMessage bool) (RefineOutcome, error) {
	for _, site := range o.sites {
		if site.Path != path || index >= len(site.Refinements) {
			continue
		}
		refinement := site.Refinements[index]
		spec := refinement.Check
		spec.record("check", o.calls)
		var ok bool
		switch spec.Fn {
		case "always":
			ok = true
		case "never":
			ok = false
		case "lengthAtLeast":
			text, _ := value.(string)
			ok = float64(len(text)) >= spec.N
		case "equals":
			var want any
			_ = json.Unmarshal(spec.Value, &want)
			ok = reflect.DeepEqual(value, want)
		case "even":
			number, _ := value.(float64)
			ok = math.Mod(number, 2) == 0
		case "greaterThan":
			number, _ := value.(float64)
			ok = number > spec.N
		case "throws", "throwsText":
			return RefineOutcome{}, errors.New(spec.Message)
		default:
			return RefineOutcome{}, fmt.Errorf("no predicate %s", spec.Fn)
		}
		if ok || !withMessage {
			return RefineOutcome{OK: ok}, nil
		}
		message := "Refine Error"
		if refinement.Error != nil {
			refinement.Error.record("error", o.calls)
			switch refinement.Error.Fn {
			case "text":
				message = refinement.Error.Text
			case "textWithValue":
				if text, isString := value.(string); isString {
					message = refinement.Error.Prefix + text
				} else if number, isNumber := value.(float64); isNumber {
					message = refinement.Error.Prefix + strconv.FormatFloat(number, 'f', -1, 64)
				}
			case "errorThrows":
				return RefineOutcome{}, errors.New(refinement.Error.Message)
			}
		}
		return RefineOutcome{OK: false, Message: message}, nil
	}
	return RefineOutcome{}, fmt.Errorf("no refinement %d at %q", index, path)
}

func TestValidateToolArgumentsRefinedMatchesPi(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "test", "parity", "sdk-vectors", "pi-ai", "validateToolArguments.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name   string
			Args   []json.RawMessage
			Result struct {
				Args       json.RawMessage `json:"args"`
				Error      *string         `json:"error"`
				Calls      []string        `json:"calls"`
				Schema     string          `json:"schema"`
				Validation string          `json:"validation"`
				Sites      []oracleSite    `json:"sites"`
			}
		}
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range file.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var toolCall struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(c.Args[1], &toolCall); err != nil {
				t.Fatal(err)
			}
			var calls []string
			refiner := oracleRefiner{sites: c.Result.Sites, calls: &calls}
			validated, err := ValidateToolArgumentsRefined(context.Background(), toolCall.Name, json.RawMessage(c.Result.Validation), toolCall.Arguments, refiner)
			wantCalls := c.Result.Calls
			if wantCalls == nil {
				wantCalls = []string{}
			}
			if calls == nil {
				calls = []string{}
			}
			switch {
			case c.Result.Error != nil:
				if err == nil || err.Error() != *c.Result.Error {
					t.Errorf("error %v\n want %q", err, *c.Result.Error)
				}
			case err != nil:
				t.Errorf("error %v, Pi accepts", err)
			default:
				var got, want any
				_ = json.Unmarshal(validated, &got)
				_ = json.Unmarshal(c.Result.Args, &want)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("arguments %s, Pi %s", validated, c.Result.Args)
				}
			}
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Errorf("calls %v\n  Pi %v", calls, wantCalls)
			}
		})
	}
}
