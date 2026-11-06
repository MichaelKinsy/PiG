//go:build parity

package runner

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// JSONPigOnlyRecordRule names JSONL records that an approved numbered divergence makes Pig emit and Pi never emit. Its counterpart of
// JSONAliasRule removes whole records from Pig's output before the comparison, so the rule is the only place a record is dropped.
// Each run of the scenario must carry exactly Count records equal to Record in Pig's output, and each must sit immediately before a
// record of type BeforeType: a run with more or fewer, or one emitted at another position, fails, so the rule can neither hide a second
// occurrence or a moved emission nor outlive the divergence it cites.
type JSONPigOnlyRecordRule struct {
	Divergence string `toml:"divergence"`  // numbered divergence, for example D250
	Record     string `toml:"record"`      // the complete JSON record Pig emits
	Count      int    `toml:"count"`       // how many such records every run carries
	BeforeType string `toml:"before_type"` // the "type" of the record that must follow each of them directly
	Reason     string `toml:"reason"`
}

var divergenceID = regexp.MustCompile(`^D[1-9][0-9]*$`)

// validate reports why the rule cannot be applied.
func (r JSONPigOnlyRecordRule) validate() error {
	switch {
	case !divergenceID.MatchString(r.Divergence):
		return fmt.Errorf("json_pig_only_records divergence %q is not a divergence number such as D250", r.Divergence)
	case strings.TrimSpace(r.Reason) == "":
		return fmt.Errorf("json_pig_only_records %s requires a reason", r.Divergence)
	case r.Count < 1:
		return fmt.Errorf("json_pig_only_records %s requires count of at least 1", r.Divergence)
	case r.BeforeType == "":
		return fmt.Errorf("json_pig_only_records %s requires before_type, the record type that follows the dropped record", r.Divergence)
	}
	if _, err := parseJSONRecords(r.Record); err != nil || strings.Contains(r.Record, "\n") {
		return fmt.Errorf("json_pig_only_records %s record is not one JSON object: %v", r.Divergence, err)
	}
	return nil
}

// recordType returns the "type" member of a JSONL record, or "" when the line has none.
func recordType(line string) string {
	var record struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(line), &record) != nil {
		return ""
	}
	return record.Type
}

// dropPigOnlyRecords returns Pig's result without the records its rules name. A rule whose record Pig did not emit exactly Count times,
// or emitted anywhere but directly before a BeforeType record, fails.
func dropPigOnlyRecords(pig Result, rules []JSONPigOnlyRecordRule) (Result, error) {
	if len(rules) == 0 {
		return pig, nil
	}
	for _, rule := range rules {
		if err := rule.validate(); err != nil {
			return pig, err
		}
	}
	text := pig.Output
	if pig.RawOutput != "" {
		text = pig.RawOutput
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	counts := make([]int, len(rules))
	kept := lines[:0:0]
	for at, line := range lines {
		dropped := false
		for i, rule := range rules {
			if compareJSONResults(Result{Output: line}, Result{Output: rule.Record}, nil) != nil {
				continue
			}
			next := ""
			if at+1 < len(lines) {
				next = recordType(lines[at+1])
			}
			if next != rule.BeforeType {
				return pig, fmt.Errorf("divergence %s: pig emitted %s before a %q record (record %d), expected one before a %q record", rule.Divergence, rule.Record, next, at+2, rule.BeforeType)
			}
			counts[i]++
			dropped = true
			break
		}
		if !dropped {
			kept = append(kept, line)
		}
	}
	for i, rule := range rules {
		if counts[i] != rule.Count {
			return pig, fmt.Errorf("divergence %s: pig emitted %d records %s, expected %d", rule.Divergence, counts[i], rule.Record, rule.Count)
		}
	}
	// An output with every record dropped stays empty, which the comparison reports as empty JSONL rather than as a match.
	rest := strings.Join(kept, "\n")
	if len(kept) > 0 {
		rest += "\n"
	}
	pig.Output, pig.RawOutput = rest, ""
	return pig, nil
}
