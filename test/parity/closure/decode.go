package closure

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	maxInputBytes  = 64 << 20
	maxRecordBytes = 4 << 20
)

func DecodeJSONL(input io.Reader) ([]Record, error) {
	return decodeJSONL(input, false)
}

func decodeStoredJSONL(input io.Reader) ([]Record, error) {
	return decodeJSONL(input, true)
}

func decodeJSONL(input io.Reader, allowAttestation bool) ([]Record, error) {
	data, err := io.ReadAll(io.LimitReader(input, maxInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read closure records: %w", err)
	}
	if len(data) > maxInputBytes {
		return nil, fmt.Errorf("closure input exceeds %d bytes", maxInputBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var records []Record
	for index := 1; ; index++ {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode closure record %d: %w", index, err)
		}
		if len(raw) > maxRecordBytes {
			return nil, fmt.Errorf("closure record %d exceeds %d bytes", index, maxRecordBytes)
		}
		var header struct {
			Kind           Kind   `json:"kind"`
			HypothesisType string `json:"hypothesisType"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return nil, fmt.Errorf("decode closure record %d kind: %w", index, err)
		}
		if header.Kind == "verdict" {
			return nil, fmt.Errorf("record kind %q is not a canonical input", header.Kind)
		}
		if (header.Kind == KindEvidenceAttestation || header.Kind == KindMutationRun || header.Kind == KindMutationAttestation || header.Kind == KindScenarioRun) && !allowAttestation {
			return nil, fmt.Errorf("record kind %q is executor-imported only", header.Kind)
		}
		if header.Kind == KindHypothesis && strings.HasPrefix(header.HypothesisType, "agent:") && !allowAttestation {
			return nil, fmt.Errorf("agent hypothesis is bundle-imported only")
		}
		record, err := newRecord(header.Kind)
		if err != nil {
			return nil, fmt.Errorf("decode closure record %d: %w", index, err)
		}
		strict := json.NewDecoder(bytes.NewReader(raw))
		strict.DisallowUnknownFields()
		if err := strict.Decode(record); err != nil {
			return nil, fmt.Errorf("decode closure record %d: %w", index, err)
		}
		var trailing any
		if err := strict.Decode(&trailing); err != io.EOF {
			if err == nil {
				return nil, fmt.Errorf("decode closure record %d: trailing JSON value", index)
			}
			return nil, fmt.Errorf("decode closure record %d trailing data: %w", index, err)
		}
		records = append(records, record)
	}
	return records, nil
}

func newRecord(kind Kind) (Record, error) {
	switch kind {
	case KindSnapshot:
		return &Snapshot{}, nil
	case KindPin:
		return &Pin{}, nil
	case KindFact:
		return &Fact{}, nil
	case KindHypothesis:
		return &Hypothesis{}, nil
	case KindProvisionalClaim:
		return &ProvisionalClaim{}, nil
	case KindFacet:
		return &Facet{}, nil
	case KindRule:
		return &Rule{}, nil
	case KindBehavior:
		return &Behavior{}, nil
	case KindObligation:
		return &Obligation{}, nil
	case KindTarget:
		return &Target{}, nil
	case KindMapping:
		return &Mapping{}, nil
	case KindReachability:
		return &Reachability{}, nil
	case KindTest:
		return &Test{}, nil
	case KindAssertion:
		return &Assertion{}, nil
	case KindExecutionWitness:
		return &ExecutionWitness{}, nil
	case KindEvidenceRequest:
		return &EvidenceRequest{}, nil
	case KindEvidenceRun:
		return &EvidenceRun{}, nil
	case KindEvidenceAttestation:
		return &EvidenceAttestation{}, nil
	case KindMutant:
		return &Mutant{}, nil
	case KindMutationRequest:
		return &MutationRequest{}, nil
	case KindMutationRun:
		return &MutationRun{}, nil
	case KindMutationAttestation:
		return &MutationAttestation{}, nil
	case KindDecision:
		return &Decision{}, nil
	case KindLease:
		return &Lease{}, nil
	case KindScenarioRun:
		return &ScenarioRun{}, nil
	case "":
		return nil, fmt.Errorf("record kind is empty")
	default:
		return nil, fmt.Errorf("unknown record kind %q", kind)
	}
}
