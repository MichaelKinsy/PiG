package durable

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"strings"
	"testing"
)

// defineDoc and defineDocFamily (packages/durable/src/documents.ts:36-86) return a token that is `{ definition }`: the Session reads the definition's
// kind, version, scope, history and fork back from it, runs its `migrate` and `checkpointWhen`, and refuses a version that is not a positive safe
// integer with a TypeError "Document <kind> version must be a positive integer" (documents.ts:97-101). DocToken and DocFamilyToken (types.ts:93-102) are
// those tokens, and each scope-refined name (types.ts:104-133) is the same token.
// mutation-checked: dropping a definition field when erasing, not running migrate or checkpointWhen, or accepting version 0, a negative or a non-safe version fails it
func TestDocTokensCarryTheirDefinitionAndValidateTheVersion(t *testing.T) {
	type state struct {
		N int `json:"n"`
	}
	migrated := -1
	var checkpointInfo CheckpointInfo
	common := CommonDocDefinition[state]{
		Kind: "tok.single", Version: 3, Initial: func() state { return state{} },
		Migrate: func(value JsonObject, fromVersion int) (state, error) {
			migrated = fromVersion
			return state{N: 100 + fromVersion}, nil
		},
		CheckpointWhen: func(value state, ops []Op, info CheckpointInfo) bool {
			checkpointInfo = info
			return value.N > 5
		},
	}
	var token DocToken[state] = DefineDoc(DocDefinition[state]{
		CommonDocDefinition: common,
		DocumentSemantics:   DocumentSemantics{Scope: ScopeConversation, History: HistoryRewindable, Fork: ForkAsOf},
	})
	var scoped ConversationDocToken[state] = token // a scope-refined name is the same token (types.ts:105)
	definition := scoped.AnyDefinition()
	if definition.Kind != "tok.single" || definition.Version != 3 || definition.Scope != ScopeConversation || definition.History != HistoryRewindable || definition.Fork != ForkAsOf || definition.Family {
		t.Fatalf("definition = %+v, want the one defined", definition)
	}
	value, err := definition.Migrate(delta.NewJsonObject(0), 2)
	if err != nil || migrated != 2 || value.Value("n") != float64(102) {
		t.Fatalf("migrate = %v, %v (from %d); want n=102 from version 2", value, err, migrated)
	}
	got, err := definition.CheckpointWhen(delta.NewJsonObject(0), nil, CheckpointInfo{DeltasSinceBase: 4})
	if err != nil || got || checkpointInfo.DeltasSinceBase != 4 {
		t.Fatalf("checkpointWhen = %v, %v (info %+v); want false with the info passed", got, err, checkpointInfo)
	}

	family := DefineDocFamily(DocFamilyDefinition[state, int]{
		Kind: "tok.family", Version: 1, Family: true, DocumentSemantics: DocumentSemantics{Scope: ScopeTask},
		Initial: func(seed int) state { return state{N: seed} },
	})
	var taskFamily TaskDocFamilyToken[state, int] = family
	if def := taskFamily.AnyDefinition(); def.Kind != "tok.family" || def.Scope != ScopeTask || !def.Family || def.Migrate != nil || def.CheckpointWhen != nil {
		t.Fatalf("family definition = %+v", def)
	}

	for _, version := range []int{0, -1, maxSafeInteger + 1} {
		func() {
			defer func() {
				message, _ := recover().(string)
				if want := "Document tok.bad version must be a positive integer"; !strings.Contains(message, want) {
					t.Errorf("version %d: recovered %q, want %q", version, message, want)
				}
			}()
			DefineDoc(DocDefinition[state]{CommonDocDefinition: CommonDocDefinition[state]{Kind: "tok.bad", Version: version, Initial: func() state { return state{} }}, DocumentSemantics: DocumentSemantics{Scope: ScopeSession}})
		}()
	}
	func() {
		defer func() {
			if message, _ := recover().(string); !strings.Contains(message, "Document tok.badfamily version must be a positive integer") {
				t.Errorf("family with version 0: recovered %q", message)
			}
		}()
		DefineDocFamily(DocFamilyDefinition[state, int]{Kind: "tok.badfamily", Version: 0, Family: true, DocumentSemantics: DocumentSemantics{Scope: ScopeSession}, Initial: func(int) state { return state{} }})
	}()
}
