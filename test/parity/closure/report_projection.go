package closure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	PortMapDataset            = "port-map"
	CoverageDataset           = "coverage-row"
	SemanticMappingDataset    = "semantic-mapping"
	SemanticDeltaDataset      = "semantic-delta"
	InputRenderMappingDataset = "behavior-input-mapping"
	AsyncContractDataset      = "async-contract"
	ChangedSourceDataset      = "upstream-sync"
	BehaviorContractDataset   = "behavior-contract"
	FormatOwnershipDataset    = "format-version"
)

type tableReportRow struct {
	fields []string
	state  VerdictState
	reason string
}

type jsonReportField struct {
	name  string
	value json.RawMessage
}

type jsonReportRow struct {
	subject string
	fields  []jsonReportField
	state   VerdictState
	reason  string
}

type jsonReportShape struct {
	metadataFields []string
	sections       []jsonReportSection
	claimDataset   string
}

type jsonReportSection struct {
	rowsField string
}

type tomlReportShape struct {
	metadataFields []string
	rowsField      string
	identityField  string
	rowFields      []string
	claimDataset   string
	nestedField    string
	nestedFields   []string
}

// ReportDatasets returns the closed dataset set accepted by the report
// engine and every Porter adapter.
func ReportDatasets() []string {
	return []string{
		PortMapDataset,
		CoverageDataset,
		SemanticMappingDataset,
		SemanticDeltaDataset,
		InputRenderMappingDataset,
		AsyncContractDataset,
		ChangedSourceDataset,
		BehaviorContractDataset,
		FormatOwnershipDataset,
		FamilyCoverageDataset,
		ScenarioQualityDataset,
		DivergenceDashboardDataset,
		FoundationDashboardDataset,
	}
}

// ReadReport renders one dataset as a graph projection. Source fields remain
// intact; state fields keep imported non-authoritative status explicit without
// allowing it to count as proof.
func ReadReport(ctx context.Context, path, dataset string) ([]byte, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	records, err := readRecords(ctx, database)
	if err != nil {
		return nil, err
	}
	graph, err := Build(records)
	if err != nil {
		return nil, err
	}
	return RenderReport(graph, dataset)
}

func RenderReport(graph *Graph, dataset string) ([]byte, error) {
	if graph == nil {
		return nil, fmt.Errorf("render report: graph is nil")
	}
	switch dataset {
	case PortMapDataset, CoverageDataset:
		return renderTableReport(graph, dataset)
	case SemanticMappingDataset, SemanticDeltaDataset, InputRenderMappingDataset:
		return renderJSONReport(graph, dataset)
	case AsyncContractDataset, ChangedSourceDataset, BehaviorContractDataset, FormatOwnershipDataset:
		return renderTOMLReport(graph, dataset)
	case FamilyCoverageDataset:
		return renderFamilyCoverageReport(graph)
	case ScenarioQualityDataset:
		return renderScenarioQualityReport(graph)
	case DivergenceDashboardDataset:
		return renderDivergenceDashboard(graph)
	case FoundationDashboardDataset:
		return renderFoundationDashboard(graph)
	default:
		return nil, fmt.Errorf("report dataset %q is unsupported", dataset)
	}
}

func renderTableReport(graph *Graph, dataset string) ([]byte, error) {
	fieldCount, header, claimDataset, err := tableReportShape(dataset)
	if err != nil {
		return nil, err
	}
	claims, err := reportClaims(graph, dataset, claimDataset)
	if err != nil {
		return nil, err
	}
	orders, subjectSections, err := reportRowOrders(graph, dataset, []jsonReportSection{{rowsField: "rows"}})
	if err != nil {
		return nil, err
	}
	rows := make([]tableReportRow, 0)
	seenSubjects := make(map[string]struct{})
	for _, id := range graph.recordIDs(KindFact) {
		fact := graph.Records[id].(*Fact)
		if fact.FactType != "denominator:"+dataset {
			continue
		}
		var value struct {
			Fields []string `json:"fields"`
		}
		if err := json.Unmarshal(fact.Value, &value); err != nil || len(value.Fields) != fieldCount {
			return nil, fmt.Errorf("report %s fact %s has invalid fields", dataset, fact.ID)
		}
		if strings.Trim(value.Fields[0], "`") != fact.SubjectID {
			return nil, fmt.Errorf("report %s fact %s subject does not match fields", dataset, fact.ID)
		}
		if _, duplicate := seenSubjects[fact.SubjectID]; duplicate {
			return nil, fmt.Errorf("report %s has duplicate row subject %s", dataset, fact.SubjectID)
		}
		if subjectSections[fact.SubjectID] != "rows" {
			return nil, fmt.Errorf("report %s row subject %s has no order entry", dataset, fact.SubjectID)
		}
		seenSubjects[fact.SubjectID] = struct{}{}
		row := tableReportRow{fields: value.Fields, state: VerdictOpen, reason: "source row has no imported status claim"}
		if claim, ok := claims[fact.SubjectID]; ok {
			row.state = VerdictProvisional
			row.reason = "imported status " + claim.Status
			delete(claims, fact.SubjectID)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("report %s has no rows", dataset)
	}
	if err := rejectOrphanClaims(dataset, claims); err != nil {
		return nil, err
	}
	order := orders["rows"]
	if len(rows) != len(order) {
		return nil, fmt.Errorf("report %s has %d rows, want %d", dataset, len(rows), len(order))
	}
	rowsBySubject := make(map[string]tableReportRow, len(rows))
	for _, row := range rows {
		rowsBySubject[strings.Trim(row.fields[0], "`")] = row
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "| %s | proven | waived | provisional | open | contradicted | reason |\n", strings.Join(header, " | "))
	output.WriteByte('|')
	for range header {
		output.WriteString("---|")
	}
	for range 5 {
		output.WriteString("---:|")
	}
	output.WriteString("---|\n")
	for _, subject := range order {
		row, exists := rowsBySubject[subject]
		if !exists {
			return nil, fmt.Errorf("report %s order subject %s has no row", dataset, subject)
		}
		for index, field := range row.fields {
			if strings.ContainsAny(field, "|\t\r\n") {
				return nil, fmt.Errorf("report %s field %d contains a report delimiter", dataset, index)
			}
		}
		if err := validateReportField("report reason", row.reason); err != nil {
			return nil, err
		}
		fmt.Fprintf(&output, "| %s | %d | %d | %d | %d | %d | %s |\n",
			strings.Join(row.fields, " | "),
			stateColumn(row.state, VerdictProven), stateColumn(row.state, VerdictWaived),
			stateColumn(row.state, VerdictProvisional), stateColumn(row.state, VerdictOpen),
			stateColumn(row.state, VerdictContradicted), row.reason)
	}
	return output.Bytes(), nil
}

func renderJSONReport(graph *Graph, dataset string) ([]byte, error) {
	shape, err := jsonReportDatasetShape(dataset)
	if err != nil {
		return nil, err
	}
	claims, err := reportClaims(graph, dataset, shape.claimDataset)
	if err != nil {
		return nil, err
	}
	orders, subjectSections, err := reportRowOrders(graph, dataset, shape.sections)
	if err != nil {
		return nil, err
	}
	var metadata map[string]json.RawMessage
	sectionRows := make(map[string]map[string]jsonReportRow, len(shape.sections))
	for _, section := range shape.sections {
		sectionRows[section.rowsField] = make(map[string]jsonReportRow)
	}
	seenSubjects := make(map[string]struct{})
	for _, recordID := range graph.recordIDs(KindFact) {
		fact := graph.Records[recordID].(*Fact)
		if fact.FactType != "denominator:"+dataset {
			continue
		}
		fields, err := decodeJSONObject(fact.Value)
		if err != nil {
			return nil, fmt.Errorf("report %s fact %s has invalid fields: %w", dataset, fact.ID, err)
		}
		if fact.SubjectID == "<metadata>" {
			if metadata != nil {
				return nil, fmt.Errorf("report %s has duplicate metadata", dataset)
			}
			metadata, err = reportMetadata(fields, shape.metadataFields)
			if err != nil {
				return nil, fmt.Errorf("report %s metadata: %w", dataset, err)
			}
			continue
		}
		if _, duplicate := seenSubjects[fact.SubjectID]; duplicate {
			return nil, fmt.Errorf("report %s has duplicate row subject %s", dataset, fact.SubjectID)
		}
		seenSubjects[fact.SubjectID] = struct{}{}
		rowsField, exists := subjectSections[fact.SubjectID]
		if !exists {
			return nil, fmt.Errorf("report %s row subject %s has no order entry", dataset, fact.SubjectID)
		}
		if err := validateJSONReportRow(dataset, fact, fields); err != nil {
			return nil, err
		}
		row := jsonReportRow{subject: fact.SubjectID, fields: fields, state: VerdictOpen, reason: "source row has no imported status claim"}
		if claim, ok := claims[fact.SubjectID]; ok {
			row.state = VerdictProvisional
			row.reason = "imported status " + claim.Status
			delete(claims, fact.SubjectID)
		}
		sectionRows[rowsField][fact.SubjectID] = row
	}
	if metadata == nil {
		return nil, fmt.Errorf("report %s has no metadata", dataset)
	}
	if err := rejectOrphanClaims(dataset, claims); err != nil {
		return nil, err
	}

	var output bytes.Buffer
	output.WriteString("{\n")
	for _, name := range shape.metadataFields {
		if err := writeJSONField(&output, "  ", name, metadata[name], true); err != nil {
			return nil, fmt.Errorf("report %s metadata field %s: %w", dataset, name, err)
		}
	}
	for sectionIndex, section := range shape.sections {
		rows := sectionRows[section.rowsField]
		order := orders[section.rowsField]
		if len(rows) != len(order) {
			return nil, fmt.Errorf("report %s section %s has %d rows, want %d", dataset, section.rowsField, len(rows), len(order))
		}
		fmt.Fprintf(&output, "  %q: [\n", section.rowsField)
		for rowIndex, subject := range order {
			row, exists := rows[subject]
			if !exists {
				return nil, fmt.Errorf("report %s order subject %s has no row", dataset, subject)
			}
			if err := writeJSONReportRow(&output, dataset, row, rowIndex < len(order)-1); err != nil {
				return nil, err
			}
		}
		output.WriteString("  ]")
		if sectionIndex < len(shape.sections)-1 {
			output.WriteByte(',')
		}
		output.WriteByte('\n')
	}
	output.WriteString("}\n")
	return output.Bytes(), nil
}

func renderTOMLReport(graph *Graph, dataset string) ([]byte, error) {
	shape, err := tomlReportDatasetShape(dataset)
	if err != nil {
		return nil, err
	}
	claims, err := reportClaims(graph, dataset, shape.claimDataset)
	if err != nil {
		return nil, err
	}
	sections := []jsonReportSection{{rowsField: shape.rowsField}}
	orders, subjectSections, err := reportRowOrders(graph, dataset, sections)
	if err != nil {
		return nil, err
	}
	var metadata map[string]json.RawMessage
	if len(shape.metadataFields) == 0 {
		metadata = make(map[string]json.RawMessage)
	}
	rows := make(map[string]jsonReportRow)
	for _, recordID := range graph.recordIDs(KindFact) {
		fact := graph.Records[recordID].(*Fact)
		if fact.FactType != "denominator:"+dataset {
			continue
		}
		fields, err := decodeJSONObject(fact.Value)
		if err != nil {
			return nil, fmt.Errorf("report %s fact %s has invalid fields: %w", dataset, fact.ID, err)
		}
		if fact.SubjectID == "<metadata>" {
			if metadata != nil {
				return nil, fmt.Errorf("report %s has duplicate metadata", dataset)
			}
			metadata, err = reportMetadata(fields, shape.metadataFields)
			if err != nil {
				return nil, fmt.Errorf("report %s metadata: %w", dataset, err)
			}
			continue
		}
		if subjectSections[fact.SubjectID] != shape.rowsField {
			return nil, fmt.Errorf("report %s row subject %s has no order entry", dataset, fact.SubjectID)
		}
		if _, duplicate := rows[fact.SubjectID]; duplicate {
			return nil, fmt.Errorf("report %s has duplicate row subject %s", dataset, fact.SubjectID)
		}
		if err := validateTOMLReportRow(dataset, fact, fields, shape); err != nil {
			return nil, err
		}
		row := jsonReportRow{subject: fact.SubjectID, fields: fields, state: VerdictOpen, reason: "source row has no imported status claim"}
		if claim, ok := claims[fact.SubjectID]; ok {
			row.state = VerdictProvisional
			row.reason = "imported status " + claim.Status
			delete(claims, fact.SubjectID)
		}
		rows[fact.SubjectID] = row
	}
	if metadata == nil {
		return nil, fmt.Errorf("report %s has no metadata", dataset)
	}
	if err := rejectOrphanClaims(dataset, claims); err != nil {
		return nil, err
	}
	order := orders[shape.rowsField]
	if len(rows) != len(order) {
		return nil, fmt.Errorf("report %s section %s has %d rows, want %d", dataset, shape.rowsField, len(rows), len(order))
	}

	var output bytes.Buffer
	for _, name := range shape.metadataFields {
		value, err := tomlValue(metadata[name])
		if err != nil {
			return nil, fmt.Errorf("report %s metadata field %s: %w", dataset, name, err)
		}
		fmt.Fprintf(&output, "%s = %s\n", name, value)
	}
	if len(shape.metadataFields) > 0 && len(order) > 0 {
		output.WriteByte('\n')
	}
	for rowIndex, subject := range order {
		row, exists := rows[subject]
		if !exists {
			return nil, fmt.Errorf("report %s order subject %s has no row", dataset, subject)
		}
		fmt.Fprintf(&output, "[[%s]]\n", shape.rowsField)
		fieldsByName := make(map[string]json.RawMessage, len(row.fields))
		for _, field := range row.fields {
			fieldsByName[field.name] = field.value
		}
		for _, name := range shape.rowFields {
			value, err := tomlValue(fieldsByName[name])
			if err != nil {
				return nil, fmt.Errorf("report %s row %s field %s: %w", dataset, subject, name, err)
			}
			fmt.Fprintf(&output, "%s = %s\n", name, value)
		}
		fmt.Fprintf(&output, "proven = %d\nwaived = %d\nprovisional = %d\nopen = %d\ncontradicted = %d\n",
			stateColumn(row.state, VerdictProven), stateColumn(row.state, VerdictWaived),
			stateColumn(row.state, VerdictProvisional), stateColumn(row.state, VerdictOpen),
			stateColumn(row.state, VerdictContradicted))
		reason, err := tomlString(row.reason)
		if err != nil {
			return nil, fmt.Errorf("report %s row %s reason: %w", dataset, subject, err)
		}
		fmt.Fprintf(&output, "reason = %s\n", reason)
		if shape.nestedField != "" {
			nestedFields, err := nestedTOMLFields(dataset, subject, fieldsByName[shape.nestedField], shape.nestedFields)
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&output, "\n[%s.%s]\n", shape.rowsField, shape.nestedField)
			for _, name := range shape.nestedFields {
				value, err := tomlValue(nestedFields[name])
				if err != nil {
					return nil, fmt.Errorf("report %s row %s nested field %s: %w", dataset, subject, name, err)
				}
				fmt.Fprintf(&output, "%s = %s\n", name, value)
			}
		}
		if rowIndex < len(order)-1 {
			output.WriteByte('\n')
		}
	}
	return output.Bytes(), nil
}

func validateTOMLReportRow(dataset string, fact *Fact, fields []jsonReportField, shape tomlReportShape) error {
	allowed := make(map[string]struct{}, len(shape.rowFields)+1)
	values := make(map[string]json.RawMessage, len(fields))
	for _, name := range shape.rowFields {
		allowed[name] = struct{}{}
	}
	if shape.nestedField != "" {
		allowed[shape.nestedField] = struct{}{}
	}
	for _, field := range fields {
		if _, exists := allowed[field.name]; !exists {
			return fmt.Errorf("report %s fact %s has unknown field %s", dataset, fact.ID, field.name)
		}
		values[field.name] = field.value
	}
	for _, name := range shape.rowFields {
		if _, exists := values[name]; !exists {
			return fmt.Errorf("report %s fact %s is missing field %s", dataset, fact.ID, name)
		}
	}
	if shape.nestedField != "" {
		if _, exists := values[shape.nestedField]; !exists {
			return fmt.Errorf("report %s fact %s is missing field %s", dataset, fact.ID, shape.nestedField)
		}
	}
	var identity string
	if err := json.Unmarshal(values[shape.identityField], &identity); err != nil || identity != fact.SubjectID {
		return fmt.Errorf("report %s fact %s subject does not match %s", dataset, fact.ID, shape.identityField)
	}
	return validateReportField("report subject", identity)
}

func nestedTOMLFields(dataset, subject string, raw json.RawMessage, expected []string) (map[string]json.RawMessage, error) {
	fields, err := decodeJSONObject(raw)
	if err != nil {
		return nil, fmt.Errorf("report %s row %s has invalid nested fields: %w", dataset, subject, err)
	}
	if len(fields) != len(expected) {
		return nil, fmt.Errorf("report %s row %s has %d nested fields, want %d", dataset, subject, len(fields), len(expected))
	}
	values := make(map[string]json.RawMessage, len(fields))
	for _, field := range fields {
		values[field.name] = field.value
	}
	for _, name := range expected {
		if _, exists := values[name]; !exists {
			return nil, fmt.Errorf("report %s row %s is missing nested field %s", dataset, subject, name)
		}
	}
	return values, nil
}

func tomlValue(raw json.RawMessage) (string, error) {
	var scalar string
	if err := json.Unmarshal(raw, &scalar); err == nil {
		return tomlString(scalar)
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return "", fmt.Errorf("expected string or string array")
	}
	encoded := make([]string, len(values))
	for index, value := range values {
		var err error
		encoded[index], err = tomlString(value)
		if err != nil {
			return "", err
		}
	}
	return "[" + strings.Join(encoded, ", ") + "]", nil
}

func tomlString(value string) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func writeJSONReportRow(output *bytes.Buffer, dataset string, row jsonReportRow, comma bool) error {
	output.WriteString("    {\n")
	for _, field := range row.fields {
		if err := writeJSONField(output, "      ", field.name, field.value, true); err != nil {
			return fmt.Errorf("report %s row %s field %s: %w", dataset, row.subject, field.name, err)
		}
	}
	stateFields := []jsonReportField{
		{name: "proven", value: json.RawMessage(fmt.Sprintf("%d", stateColumn(row.state, VerdictProven)))},
		{name: "waived", value: json.RawMessage(fmt.Sprintf("%d", stateColumn(row.state, VerdictWaived)))},
		{name: "provisional", value: json.RawMessage(fmt.Sprintf("%d", stateColumn(row.state, VerdictProvisional)))},
		{name: "open", value: json.RawMessage(fmt.Sprintf("%d", stateColumn(row.state, VerdictOpen)))},
		{name: "contradicted", value: json.RawMessage(fmt.Sprintf("%d", stateColumn(row.state, VerdictContradicted)))},
	}
	for _, field := range stateFields {
		if err := writeJSONField(output, "      ", field.name, field.value, true); err != nil {
			return err
		}
	}
	reason, err := json.Marshal(row.reason)
	if err != nil {
		return fmt.Errorf("report %s row %s reason: %w", dataset, row.subject, err)
	}
	if err := writeJSONField(output, "      ", "reason", reason, false); err != nil {
		return err
	}
	output.WriteString("    }")
	if comma {
		output.WriteByte(',')
	}
	output.WriteByte('\n')
	return nil
}

func reportRowOrders(graph *Graph, dataset string, sections []jsonReportSection) (map[string][]string, map[string]string, error) {
	orderFacts := make(map[string]*Fact, len(sections))
	for _, recordID := range graph.recordIDs(KindFact) {
		fact := graph.Records[recordID].(*Fact)
		if fact.FactType != "denominator-order:"+dataset {
			continue
		}
		if existing := orderFacts[fact.SubjectID]; existing != nil {
			return nil, nil, fmt.Errorf("report %s has duplicate order facts for %s", dataset, fact.SubjectID)
		}
		orderFacts[fact.SubjectID] = fact
	}
	if len(orderFacts) != len(sections) {
		return nil, nil, fmt.Errorf("report %s has %d row order facts, want %d", dataset, len(orderFacts), len(sections))
	}
	orders := make(map[string][]string, len(sections))
	subjectSections := make(map[string]string)
	for _, section := range sections {
		orderFact := orderFacts[section.rowsField]
		if orderFact == nil {
			return nil, nil, fmt.Errorf("report %s has no row order for %s", dataset, section.rowsField)
		}
		fields, err := decodeJSONObject(orderFact.Value)
		if err != nil || len(fields) != 1 || fields[0].name != "subjects" {
			return nil, nil, fmt.Errorf("report %s order fact %s has invalid fields", dataset, orderFact.ID)
		}
		var subjects []string
		if err := json.Unmarshal(fields[0].value, &subjects); err != nil || subjects == nil {
			return nil, nil, fmt.Errorf("report %s order fact %s has invalid subjects", dataset, orderFact.ID)
		}
		for _, subject := range subjects {
			if subject == "" {
				return nil, nil, fmt.Errorf("report %s order fact %s has an empty subject", dataset, orderFact.ID)
			}
			if err := validateReportField("report order subject", subject); err != nil {
				return nil, nil, err
			}
			if existing, duplicate := subjectSections[subject]; duplicate {
				return nil, nil, fmt.Errorf("report %s order repeats subject %s in %s and %s", dataset, subject, existing, section.rowsField)
			}
			subjectSections[subject] = section.rowsField
		}
		orders[section.rowsField] = subjects
	}
	return orders, subjectSections, nil
}

func reportClaims(graph *Graph, reportDataset, claimDataset string) (map[string]ProvisionalClaim, error) {
	claims := make(map[string]ProvisionalClaim)
	for _, id := range graph.recordIDs(KindProvisionalClaim) {
		claim := graph.Records[id].(*ProvisionalClaim)
		if !strings.HasPrefix(claim.ID, "provisional:denominator:"+claimDataset+":") {
			continue
		}
		if err := validateReportField("imported claim subject", claim.SubjectID); err != nil {
			return nil, err
		}
		if err := validateReportField("imported claim status", claim.Status); err != nil {
			return nil, err
		}
		if existing, exists := claims[claim.SubjectID]; exists {
			return nil, fmt.Errorf("report %s has duplicate claim subject %s (%s, %s)", reportDataset, claim.SubjectID, existing.ID, claim.ID)
		}
		claims[claim.SubjectID] = *claim
	}
	return claims, nil
}

func rejectOrphanClaims(dataset string, claims map[string]ProvisionalClaim) error {
	if len(claims) == 0 {
		return nil
	}
	subjects := make([]string, 0, len(claims))
	for subject := range claims {
		subjects = append(subjects, subject)
	}
	slices.Sort(subjects)
	return fmt.Errorf("report %s claim subject %s has no row", dataset, subjects[0])
}

func decodeJSONObject(value []byte) ([]jsonReportField, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	start, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := start.(json.Delim); !ok || delimiter != '{' {
		return nil, fmt.Errorf("expected object")
	}
	fields := make([]jsonReportField, 0)
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("invalid field name")
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("duplicate field %s", name)
		}
		seen[name] = struct{}{}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		fields = append(fields, jsonReportField{name: name, value: raw})
	}
	end, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := end.(json.Delim); !ok || delimiter != '}' {
		return nil, fmt.Errorf("object is not closed")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON")
		}
		return nil, err
	}
	return fields, nil
}

func reportMetadata(fields []jsonReportField, expected []string) (map[string]json.RawMessage, error) {
	if len(fields) != len(expected) {
		return nil, fmt.Errorf("has %d fields, want %d", len(fields), len(expected))
	}
	values := make(map[string]json.RawMessage, len(fields))
	for _, field := range fields {
		values[field.name] = field.value
	}
	for _, name := range expected {
		if _, exists := values[name]; !exists {
			return nil, fmt.Errorf("missing field %s", name)
		}
	}
	return values, nil
}

func validateJSONReportRow(dataset string, fact *Fact, fields []jsonReportField) error {
	reserved := map[string]struct{}{
		"proven": {}, "waived": {}, "provisional": {}, "open": {}, "contradicted": {}, "reason": {},
	}
	var identity string
	for _, field := range fields {
		if _, conflict := reserved[field.name]; conflict {
			return fmt.Errorf("report %s fact %s contains reserved state field %s", dataset, fact.ID, field.name)
		}
		if field.name != "id" {
			continue
		}
		if err := json.Unmarshal(field.value, &identity); err != nil || identity == "" {
			return fmt.Errorf("report %s fact %s has invalid id", dataset, fact.ID)
		}
	}
	if identity != fact.SubjectID {
		return fmt.Errorf("report %s fact %s subject does not match id", dataset, fact.ID)
	}
	if err := validateReportField("report subject", identity); err != nil {
		return err
	}
	return nil
}

func writeJSONField(output *bytes.Buffer, indent, name string, value json.RawMessage, comma bool) error {
	encodedName, err := json.Marshal(name)
	if err != nil {
		return err
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, value, indent, "  "); err != nil {
		return err
	}
	output.WriteString(indent)
	output.Write(encodedName)
	output.WriteString(": ")
	output.Write(indented.Bytes())
	if comma {
		output.WriteByte(',')
	}
	output.WriteByte('\n')
	return nil
}

func tableReportShape(dataset string) (int, []string, string, error) {
	switch dataset {
	case PortMapDataset:
		return 3, []string{"upstream", "pig", "status"}, PortMapDataset, nil
	case CoverageDataset:
		return 5, []string{"upstream", "port", "scenarios", "behavioral", "last run"}, PortMapDataset, nil
	default:
		return 0, nil, "", fmt.Errorf("report dataset %q is unsupported", dataset)
	}
}

func jsonReportDatasetShape(dataset string) (jsonReportShape, error) {
	switch dataset {
	case SemanticMappingDataset:
		return jsonReportShape{
			metadataFields: []string{"upstreamVersion"},
			sections:       []jsonReportSection{{rowsField: "mappings"}},
			claimDataset:   SemanticMappingDataset,
		}, nil
	case SemanticDeltaDataset:
		return jsonReportShape{
			metadataFields: []string{"from", "to"},
			sections:       []jsonReportSection{{rowsField: "changes"}},
			claimDataset:   SemanticDeltaDataset,
		}, nil
	case InputRenderMappingDataset:
		return jsonReportShape{
			metadataFields: []string{"upstreamVersion"},
			sections:       []jsonReportSection{{rowsField: "mappings"}, {rowsField: "renderMappings"}},
			claimDataset:   InputRenderMappingDataset,
		}, nil
	default:
		return jsonReportShape{}, fmt.Errorf("report dataset %q is unsupported", dataset)
	}
}

func tomlReportDatasetShape(dataset string) (tomlReportShape, error) {
	switch dataset {
	case AsyncContractDataset:
		return tomlReportShape{
			metadataFields: []string{"version"},
			rowsField:      "files",
			identityField:  "path",
			rowFields:      []string{"path", "disposition", "contracts", "evidence", "rationale"},
			claimDataset:   AsyncContractDataset,
		}, nil
	case ChangedSourceDataset:
		return tomlReportShape{
			metadataFields: []string{"from", "to"},
			rowsField:      "files",
			identityField:  "path",
			rowFields:      []string{"path", "change", "disposition", "evidence", "rationale"},
			claimDataset:   ChangedSourceDataset,
		}, nil
	case BehaviorContractDataset:
		return tomlReportShape{
			metadataFields: []string{"upstream_version"},
			rowsField:      "contract",
			identityField:  "id",
			rowFields: []string{
				"id", "upstream_id", "family", "kind", "claim", "status", "pig_targets", "evidence",
			},
			claimDataset: BehaviorContractDataset,
			nestedField:  "upstream",
			nestedFields: []string{"path", "start", "end", "sha256", "keybindings"},
		}, nil
	case FormatOwnershipDataset:
		return tomlReportShape{
			rowsField:     "fields",
			identityField: "id",
			rowFields: []string{
				"id", "path", "owner", "field", "wire_name", "classification", "rationale",
			},
			claimDataset: FormatOwnershipDataset,
		}, nil
	default:
		return tomlReportShape{}, fmt.Errorf("report dataset %q is unsupported", dataset)
	}
}
