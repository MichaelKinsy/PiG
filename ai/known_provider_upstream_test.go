package ai

import (
	"slices"
	"testing"
)

// packages/ai/src/types.ts:43-85 (KnownProvider, ProviderId = KnownProvider | string): the Go constants are the upstream literals in order,
// and every one is a provider of the generated catalog (providers/all.ts getBuiltinProviders), which has no other provider.
func TestKnownProviderConstantsMatchTheUpstreamUnionAndTheCatalog(t *testing.T) {
	goValues := []KnownProvider{
		KnownProviderAmazonBedrock, KnownProviderAntLing, KnownProviderAnthropic, KnownProviderGoogle, KnownProviderGoogleVertex, KnownProviderOpenai,
		KnownProviderAzure, KnownProviderOpenaiCodex, KnownProviderRadius, KnownProviderTypesafe, KnownProviderNvidia, KnownProviderDeepseek,
		KnownProviderGithubCopilot, KnownProviderXai, KnownProviderGroq, KnownProviderCerebras, KnownProviderOpenrouter, KnownProviderVercelAiGateway,
		KnownProviderZai, KnownProviderZaiCodingCn, KnownProviderMistral, KnownProviderMinimax, KnownProviderMinimaxCn, KnownProviderMoonshotai,
		KnownProviderMoonshotaiCn, KnownProviderHuggingface, KnownProviderFireworks, KnownProviderTogether, KnownProviderBaseten, KnownProviderOpencode,
		KnownProviderOpencodeGo, KnownProviderKimiCoding, KnownProviderMeta, KnownProviderCloudflareWorkersAi, KnownProviderCloudflareAiGateway,
		KnownProviderQwenTokenPlan, KnownProviderQwenTokenPlanCn, KnownProviderQwenTokenPlanIndividual, KnownProviderXiaomi,
		KnownProviderXiaomiTokenPlanCn, KnownProviderXiaomiTokenPlanAms, KnownProviderXiaomiTokenPlanSgp,
	}
	got := make([]string, len(goValues))
	for i, value := range goValues {
		got[i] = string(value)
	}
	if want := upstreamStringUnion(t, "types.ts", "KnownProvider"); !slices.Equal(got, want) {
		t.Errorf("Go constants %v, upstream %v", got, want)
	}
	var id ProviderID = "a-provider-pi-does-not-list"
	if id == "" {
		t.Fatal("a ProviderID holds any string")
	}
	catalog := slices.Sorted(slices.Values(ListProviders()))
	if sorted := slices.Sorted(slices.Values(got)); !slices.Equal(sorted, catalog) {
		t.Errorf("KnownProvider constants %v differ from the generated catalog providers %v", sorted, catalog)
	}
}
