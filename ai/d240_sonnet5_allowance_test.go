package ai

import "slices"

// Parity allowance of D240 (Pi 4b9777737, Claude Sonnet 5 takes no temperature). The pinned pi-ai 1.5.0 catalog predates the commit, so its Sonnet 5 records on an
// anthropic-messages provider lack compat.supportsTemperature: false, which the generated Go catalog carries (cmd/gen-models applies the rule while
// generating). The comparisons with the published package accept exactly these seven records, and only that one compat member.
var d240SonnetFiveRecords = []string{
	"anthropic/claude-sonnet-5",
	"cloudflare-ai-gateway/claude-sonnet-5",
	"github-copilot/claude-sonnet-5",
	"opencode/claude-sonnet-5",
	"openrouter/anthropic/claude-sonnet-5",
	"vercel-ai-gateway/anthropic/claude-sonnet-5",
	"vercel-ai-gateway/anthropic/claude-sonnet-5-batch",
}

func d240AllowsSonnetFive(provider, id string) bool {
	return slices.Contains(d240SonnetFiveRecords, provider+"/"+id)
}

// d240PatchPublished applies the allowance to a published model record (a decoded JSON object): it adds compat.supportsTemperature false to an allowed
// record that does not set it. A record that already sets it (a pin that contains 4b9777737) is left alone.
func d240PatchPublished(model map[string]any) {
	provider, _ := model["provider"].(string)
	id, _ := model["id"].(string)
	if !d240AllowsSonnetFive(provider, id) {
		return
	}
	compat, _ := model["compat"].(map[string]any)
	if compat == nil {
		compat = map[string]any{}
		model["compat"] = compat
	}
	if _, set := compat["supportsTemperature"]; !set {
		compat["supportsTemperature"] = false
	}
}
