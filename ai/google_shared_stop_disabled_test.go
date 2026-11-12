package ai

import "testing"

// google-shared.ts:102-112 getDisabledGoogleThinkingConfig: a model without the discrete level control sends a zero budget; a level
// model sends the lowest level its map allows when "off" is not itself available, and a zero budget when "off" is.
func TestGetDisabledGoogleThinkingConfigFollowsTheModelsLevelControl(t *testing.T) {
	budgetModel := &Model{ID: "gemini-2.5-flash", ProviderMeta: ProviderMetadata{ProviderID: "test-google", Reasoning: true}}
	config, err := getDisabledGoogleThinkingConfig(budgetModel)
	if err != nil || config == nil || config.ThinkingBudget == nil || *config.ThinkingBudget != 0 || config.ThinkingLevel != "" {
		t.Fatalf("budget model = %+v, %v; want thinkingBudget 0 only", config, err)
	}
	unmappedBudgetModel := &Model{ID: "gemini-2.5-flash", ProviderMeta: ProviderMetadata{ProviderID: "test-google", Reasoning: true}, ThinkingLevelMap: ThinkingLevelMap{ThinkingOff: nil}}
	config, err = getDisabledGoogleThinkingConfig(unmappedBudgetModel)
	if err != nil || config == nil || config.ThinkingBudget == nil || *config.ThinkingBudget != 0 || config.ThinkingLevel != "" {
		t.Fatalf("budget model without off = %+v, %v; want thinkingBudget 0 only", config, err)
	}
	levelModel := &Model{ID: "gemini-3.7-flash", ProviderMeta: ProviderMetadata{ProviderID: "test-google", Reasoning: true}, ThinkingLevelMap: ThinkingLevelMap{ThinkingOff: nil}}
	config, err = getDisabledGoogleThinkingConfig(levelModel)
	if err != nil || config == nil || config.ThinkingLevel != "MINIMAL" || config.ThinkingBudget != nil {
		t.Fatalf("level model = %+v, %v; want thinkingLevel MINIMAL only", config, err)
	}
	offModel := &Model{ID: "gemini-3.7-flash", ProviderMeta: ProviderMetadata{ProviderID: "test-google", Reasoning: true}}
	config, err = getDisabledGoogleThinkingConfig(offModel)
	if err != nil || config == nil || config.ThinkingBudget == nil || *config.ThinkingBudget != 0 || config.ThinkingLevel != "" {
		t.Fatalf("model with off available = %+v, %v; want thinkingBudget 0 only", config, err)
	}
}
