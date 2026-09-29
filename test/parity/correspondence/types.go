package correspondence

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	LanguageTypeScript = "typescript"
	LanguageGo         = "go"
)

type Inventory struct {
	Source    SourceIdentity `json:"source"`
	Tables    []DataTable    `json:"tables"`
	Constants []Constant     `json:"constants"`
	Functions []Function     `json:"functions"`
}

type SourceIdentity struct {
	Language string `json:"language"`
	Revision string `json:"revision"`
}

type DataTable struct {
	ID                  string               `json:"id"`
	Path                string               `json:"path"`
	Owner               string               `json:"owner"`
	SourceHash          string               `json:"sourceHash"`
	OrderProfile        string               `json:"orderProfile"`
	Items               []DataItem           `json:"items"`
	Callbacks           []DispatchCase       `json:"callbacks"`
	ProductionCallbacks []ProductionCallback `json:"productionCallbacks"`
}

type DataItem struct {
	ID                     string   `json:"id"`
	Label                  string   `json:"label"`
	Description            string   `json:"description"`
	DescriptionExpression  string   `json:"descriptionExpression"`
	CurrentValueExpression string   `json:"currentValueExpression"`
	CurrentReads           []string `json:"currentReads"`
	CurrentWrites          []string `json:"currentWrites"`
	CurrentCalls           []string `json:"currentCalls"`
	Values                 []string `json:"values"`
	ValuesExpression       string   `json:"valuesExpression"`
	SubmenuExpression      string   `json:"submenuExpression"`
	Gate                   string   `json:"gate"`
	PositionExpression     string   `json:"positionExpression"`
	Path                   string   `json:"path"`
	StartLine              int      `json:"startLine"`
	EndLine                int      `json:"endLine"`
	SourceHash             string   `json:"sourceHash"`
}

type DispatchCase struct {
	ID     string   `json:"id"`
	Reads  []string `json:"reads"`
	Writes []string `json:"writes"`
	Calls  []string `json:"calls"`
}

type ProductionCallback struct {
	ID       string          `json:"id"`
	Handler  string          `json:"handler"`
	Segments []EffectSegment `json:"segments"`
}

type EffectSegment struct {
	Role        string               `json:"role"`
	Path        string               `json:"path"`
	StartLine   int                  `json:"startLine"`
	EndLine     int                  `json:"endLine"`
	SourceHash  string               `json:"sourceHash"`
	Reads       []string             `json:"reads"`
	Writes      []string             `json:"writes"`
	Calls       []FunctionCall       `json:"calls"`
	Transitions []FunctionTransition `json:"transitions"`
}

type Constant struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Name        string `json:"name"`
	Value       string `json:"value"`
	UTF16Length int    `json:"utf16Length"`
	SourceHash  string `json:"sourceHash"`
	ValueHash   string `json:"valueHash"`
	StartLine   int    `json:"startLine"`
	EndLine     int    `json:"endLine"`
}

type Function struct {
	ID                 string               `json:"id"`
	Path               string               `json:"path"`
	Name               string               `json:"name"`
	Kind               string               `json:"kind"`
	Async              bool                 `json:"async"`
	CancellationInputs []string             `json:"cancellationInputs"`
	Calls              []FunctionCall       `json:"calls"`
	Transitions        []FunctionTransition `json:"transitions"`
	Callers            []FunctionCaller     `json:"callers"`
	StartLine          int                  `json:"startLine"`
	EndLine            int                  `json:"endLine"`
	SourceHash         string               `json:"sourceHash"`
}

type FunctionCaller struct {
	Path       string `json:"path"`
	Symbol     string `json:"symbol"`
	Expression string `json:"expression"`
	StartLine  int    `json:"startLine"`
	SourceHash string `json:"sourceHash"`
}

type FunctionCall struct {
	Ordinal    int      `json:"ordinal"`
	Callee     string   `json:"callee"`
	Awaited    bool     `json:"awaited"`
	Arguments  []string `json:"arguments"`
	Conditions []string `json:"conditions"`
	StartLine  int      `json:"startLine"`
}

type FunctionTransition struct {
	Ordinal    int      `json:"ordinal"`
	Kind       string   `json:"kind"`
	Target     string   `json:"target"`
	Expression string   `json:"expression"`
	Conditions []string `json:"conditions"`
	StartLine  int      `json:"startLine"`
}

func DecodeInventory(reader io.Reader) (*Inventory, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var inventory Inventory
	if err := decoder.Decode(&inventory); err != nil {
		return nil, fmt.Errorf("decode correspondence inventory: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	if err := inventory.Validate(); err != nil {
		return nil, err
	}
	return &inventory, nil
}

func (inventory *Inventory) Validate() error {
	if inventory.Source.Language != LanguageTypeScript && inventory.Source.Language != LanguageGo {
		return fmt.Errorf("invalid correspondence source language %q", inventory.Source.Language)
	}
	if inventory.Source.Revision == "" {
		return fmt.Errorf("correspondence source revision is required")
	}
	if inventory.Tables == nil || inventory.Constants == nil || inventory.Functions == nil {
		return fmt.Errorf("correspondence inventory collections must be arrays")
	}
	if len(inventory.Tables) == 0 && len(inventory.Constants) == 0 && len(inventory.Functions) == 0 {
		return fmt.Errorf("correspondence inventory is empty")
	}
	tableIDs := make(map[string]struct{}, len(inventory.Tables))
	for index := range inventory.Tables {
		table := &inventory.Tables[index]
		if table.ID == "" || table.Path == "" || table.Owner == "" || !validHash(table.SourceHash) || table.ProductionCallbacks == nil {
			return fmt.Errorf("correspondence table %q has incomplete identity", table.ID)
		}
		if _, exists := tableIDs[table.ID]; exists {
			return fmt.Errorf("duplicate correspondence table %s", table.ID)
		}
		tableIDs[table.ID] = struct{}{}
		itemIDs := make(map[string]struct{}, len(table.Items))
		for itemIndex := range table.Items {
			item := &table.Items[itemIndex]
			if item.ID == "" || item.Path == "" || item.StartLine < 1 || item.EndLine < item.StartLine || !validHash(item.SourceHash) {
				return fmt.Errorf("correspondence table %s item %q has incomplete identity", table.ID, item.ID)
			}
			if _, exists := itemIDs[item.ID]; exists {
				return fmt.Errorf("correspondence table %s has duplicate item %s", table.ID, item.ID)
			}
			itemIDs[item.ID] = struct{}{}
			if item.CurrentReads == nil || item.CurrentWrites == nil || item.CurrentCalls == nil || !sortedUniqueStrings(item.CurrentReads) || !sortedUniqueStrings(item.CurrentWrites) || !sortedUniqueStrings(item.CurrentCalls) {
				return fmt.Errorf("correspondence table %s item %s current effects are incomplete", table.ID, item.ID)
			}
			valueContracts := 0
			if len(item.Values) > 0 {
				valueContracts++
			}
			if item.ValuesExpression != "" {
				valueContracts++
			}
			if item.SubmenuExpression != "" {
				valueContracts++
			}
			if valueContracts != 1 {
				return fmt.Errorf("correspondence table %s item %s has invalid value contract", table.ID, item.ID)
			}
		}
		if !slices.IsSortedFunc(table.Callbacks, func(left, right DispatchCase) int { return strings.Compare(left.ID, right.ID) }) {
			return fmt.Errorf("correspondence table %s callbacks are not sorted", table.ID)
		}
		callbackIDs := make(map[string]struct{}, len(table.Callbacks))
		for _, callback := range table.Callbacks {
			if callback.ID == "" || callback.Reads == nil || callback.Writes == nil || callback.Calls == nil {
				return fmt.Errorf("correspondence table %s has incomplete callback %q", table.ID, callback.ID)
			}
			if _, exists := callbackIDs[callback.ID]; exists {
				return fmt.Errorf("correspondence table %s has duplicate callback %s", table.ID, callback.ID)
			}
			callbackIDs[callback.ID] = struct{}{}
			if !sortedUniqueStrings(callback.Reads) || !sortedUniqueStrings(callback.Writes) || !sortedUniqueStrings(callback.Calls) {
				return fmt.Errorf("correspondence table %s callback %s effects are not sorted and unique", table.ID, callback.ID)
			}
			if _, exists := itemIDs[callback.ID]; !exists {
				return fmt.Errorf("correspondence table %s callback %s has no item", table.ID, callback.ID)
			}
		}
		if len(callbackIDs) != len(itemIDs) {
			return fmt.Errorf("correspondence table %s callbacks do not cover every item", table.ID)
		}
		if !slices.IsSortedFunc(table.ProductionCallbacks, func(left, right ProductionCallback) int { return strings.Compare(left.ID, right.ID) }) {
			return fmt.Errorf("correspondence table %s production callbacks are not sorted", table.ID)
		}
		productionIDs := make(map[string]struct{}, len(table.ProductionCallbacks))
		for _, callback := range table.ProductionCallbacks {
			if callback.ID == "" || callback.Handler == "" || len(callback.Segments) == 0 {
				return fmt.Errorf("correspondence table %s has incomplete production callback %q", table.ID, callback.ID)
			}
			if _, exists := productionIDs[callback.ID]; exists {
				return fmt.Errorf("correspondence table %s has duplicate production callback %s", table.ID, callback.ID)
			}
			productionIDs[callback.ID] = struct{}{}
			if _, exists := itemIDs[callback.ID]; !exists {
				return fmt.Errorf("correspondence table %s production callback %s has no item", table.ID, callback.ID)
			}
			for index, segment := range callback.Segments {
				if segment.Role == "" || segment.Path == "" || segment.StartLine < 1 || segment.EndLine < segment.StartLine || !validHash(segment.SourceHash) || segment.Reads == nil || segment.Writes == nil || segment.Calls == nil || segment.Transitions == nil {
					return fmt.Errorf("correspondence table %s production callback %s segment %d is incomplete", table.ID, callback.ID, index)
				}
				if !sortedUniqueStrings(segment.Reads) || !sortedUniqueStrings(segment.Writes) {
					return fmt.Errorf("correspondence table %s production callback %s segment %d effects are not sorted and unique", table.ID, callback.ID, index)
				}
				if err := validateCallsTransitions(segment.Calls, segment.Transitions, segment.StartLine, segment.EndLine); err != nil {
					return fmt.Errorf("correspondence table %s production callback %s segment %d: %w", table.ID, callback.ID, index, err)
				}
			}
		}
		if len(productionIDs) != len(itemIDs) {
			return fmt.Errorf("correspondence table %s production callbacks do not cover every item", table.ID)
		}
	}
	constantIDs := make(map[string]struct{}, len(inventory.Constants))
	for _, constant := range inventory.Constants {
		if constant.ID == "" || constant.Path == "" || constant.Name == "" || constant.StartLine < 1 || constant.EndLine < constant.StartLine || !validHash(constant.SourceHash) || !validHash(constant.ValueHash) {
			return fmt.Errorf("correspondence constant %q has incomplete identity", constant.ID)
		}
		if _, exists := constantIDs[constant.ID]; exists {
			return fmt.Errorf("duplicate correspondence constant %s", constant.ID)
		}
		constantIDs[constant.ID] = struct{}{}
	}
	functionIDs := make(map[string]struct{}, len(inventory.Functions))
	functionNames := make(map[string]struct{}, len(inventory.Functions))
	if !slices.IsSortedFunc(inventory.Functions, func(left, right Function) int { return strings.Compare(left.ID, right.ID) }) {
		return fmt.Errorf("correspondence functions are not sorted")
	}
	for _, function := range inventory.Functions {
		if function.ID == "" || function.Path == "" || function.Name == "" || (function.Kind != "compaction" && function.Kind != "settings-manager" && function.Kind != "settings-orchestration") || function.StartLine < 1 || function.EndLine < function.StartLine || !validHash(function.SourceHash) || function.CancellationInputs == nil || function.Calls == nil || function.Transitions == nil || function.Callers == nil {
			return fmt.Errorf("correspondence function %q has incomplete identity", function.ID)
		}
		if _, exists := functionIDs[function.ID]; exists {
			return fmt.Errorf("duplicate correspondence function %s", function.ID)
		}
		functionIDs[function.ID] = struct{}{}
		if _, exists := functionNames[function.Name]; exists {
			return fmt.Errorf("duplicate correspondence function name %s", function.Name)
		}
		functionNames[function.Name] = struct{}{}
		if !sortedUniqueStrings(function.CancellationInputs) {
			return fmt.Errorf("correspondence function %s cancellation inputs are not sorted and unique", function.ID)
		}
		if err := validateCallsTransitions(function.Calls, function.Transitions, function.StartLine, function.EndLine); err != nil {
			return fmt.Errorf("correspondence function %s: %w", function.ID, err)
		}
		if !slices.IsSortedFunc(function.Callers, func(left, right FunctionCaller) int {
			return strings.Compare(left.Path+"\x00"+left.Symbol+"\x00"+left.Expression+fmt.Sprint(left.StartLine), right.Path+"\x00"+right.Symbol+"\x00"+right.Expression+fmt.Sprint(right.StartLine))
		}) {
			return fmt.Errorf("correspondence function %s callers are not sorted", function.ID)
		}
		for _, caller := range function.Callers {
			if caller.Path == "" || caller.Symbol == "" || caller.Expression == "" || caller.StartLine < 1 || !validHash(caller.SourceHash) {
				return fmt.Errorf("correspondence function %s has incomplete caller", function.ID)
			}
		}
	}
	return nil
}

func validateCallsTransitions(calls []FunctionCall, transitions []FunctionTransition, startLine, endLine int) error {
	for index, call := range calls {
		if call.Ordinal != index+1 || call.Callee == "" || call.Arguments == nil || call.Conditions == nil || call.StartLine < startLine || call.StartLine > endLine {
			return fmt.Errorf("invalid call %d: %#v", index+1, call)
		}
	}
	for index, transition := range transitions {
		if transition.Ordinal != index+1 || (transition.Kind != "bind" && transition.Kind != "return" && transition.Kind != "error" && transition.Kind != "update") || transition.Expression == "" || transition.Conditions == nil || transition.StartLine < startLine || transition.StartLine > endLine {
			return fmt.Errorf("invalid transition %d: %#v", index+1, transition)
		}
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode correspondence inventory suffix: %w", err)
	}
	return fmt.Errorf("decode correspondence inventory: multiple JSON values")
}

func validHash(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, digit := range value[len("sha256:"):] {
		if !strings.ContainsRune("0123456789abcdef", digit) {
			return false
		}
	}
	return true
}

func sortedUniqueStrings(values []string) bool {
	return slices.IsSorted(values) && len(slices.Compact(slices.Clone(values))) == len(values)
}
