package host

// pi: packages/chord/src/wire.ts
// Pi's wire.ts run under Node 24 on the inputs in testdata/pi_wire.json (testdata/pi_wire.mjs regenerates it from the
// pinned mirror): isFrame over a valid frame of every type and seeded edits of them, toWireError and fromWireError. Each
// case is JSON text and the verdict Pi returned; the Go code gets the same text.

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

type piWireOracle struct {
	Protocol int `json:"protocol"`
	Cases    []struct {
		Text   string `json:"text"`
		Expect bool   `json:"expect"`
	} `json:"cases"`
	WireErrors []struct {
		Kind    string      `json:"kind"`
		Code    *string     `json:"code"`
		Message string      `json:"message"`
		Reason  *string     `json:"reason"`
		Wire    piWireError `json:"wire"`
		Back    piErrorView `json:"back"`
	} `json:"wireErrors"`
	FromWire []struct {
		Wire piWireError `json:"wire"`
		Back piErrorView `json:"back"`
	} `json:"fromWire"`
}

type piWireError struct {
	Code    ChordErrorCode `json:"code"`
	Message string         `json:"message"`
	Reason  string         `json:"reason"`
}

func loadPiWireOracle(t *testing.T) piWireOracle {
	t.Helper()
	data, err := os.ReadFile("testdata/pi_wire.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle piWireOracle
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	return oracle
}

func TestIsFrameMatchesPi(t *testing.T) {
	oracle := loadPiWireOracle(t)
	if oracle.Protocol != 4 {
		t.Fatalf("Pi protocol version %d", oracle.Protocol)
	}
	valid := 0
	for _, item := range oracle.Cases {
		if item.Expect {
			valid++
		}
		if got := IsFrame(decodeChordJSON(t, []byte(item.Text))); got != item.Expect {
			t.Errorf("isFrame(%s) = %v, Pi %v", item.Text, got, item.Expect)
		}
	}
	if len(oracle.Cases) < 1000 || valid < 100 || valid > len(oracle.Cases)-100 {
		t.Fatalf("Pi oracle has %d cases, %d valid", len(oracle.Cases), valid)
	}
}

func TestWireErrorsMatchPi(t *testing.T) {
	oracle := loadPiWireOracle(t)
	for _, item := range oracle.WireErrors {
		var err error
		switch item.Kind {
		case "chord":
			options := ChordErrorOptions{}
			if item.Reason != nil {
				options.Reason = *item.Reason
			}
			err = &ChordError{Name: "ChordError", Code: ChordErrorCode(*item.Code), Reason: options.Reason, Message: item.Message}
		case "plain":
			err = errors.New(item.Message)
		case "aggregate":
			err = &AggregateError{Message: item.Message, Errors: []error{errors.New("a"), errors.New("b")}}
		}
		wire := ToWireError(err)
		if string(wire.Code) != string(item.Wire.Code) || wire.Message != item.Wire.Message || wire.Reason != item.Wire.Reason {
			t.Errorf("toWireError(%s %q) = %+v, Pi %+v", item.Kind, item.Message, wire, item.Wire)
		}
		checkPiError(t, "fromWireError("+item.Message+")", FromWireError(WireError{Code: item.Wire.Code, Message: item.Wire.Message, Reason: item.Wire.Reason}), item.Back)
	}
	for _, item := range oracle.FromWire {
		checkPiError(t, "fromWireError("+item.Wire.Message+")", FromWireError(WireError{Code: item.Wire.Code, Message: item.Wire.Message, Reason: item.Wire.Reason}), item.Back)
	}
}
