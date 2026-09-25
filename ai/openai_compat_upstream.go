// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package ai

import "strings"

// upstreamNonStandard mirrors isNonStandard in upstream detectCompat
// (openai-completions, Pi 0.87.1). Upstream sends store: false to every other
// provider, including custom ones.
func upstreamNonStandard(provider, baseURL string) bool {
	switch provider {
	case "nvidia", "cerebras", "xai", "together", "deepseek", "zai", "zai-coding-cn", "moonshotai", "moonshotai-cn", "opencode", "cloudflare-workers-ai", "cloudflare-ai-gateway", "ant-ling":
		return true
	}
	return upstreamURLHas(baseURL, "integrate.api.nvidia.com", "cerebras.ai", "api.x.ai", "api.together.ai", "api.together.xyz", "chutes.ai", "api.z.ai", "open.bigmodel.cn", "api.moonshot.", "opencode.ai", "api.cloudflare.com", "gateway.ai.cloudflare.com", "api.ant-ling.com") ||
		strings.Contains(strings.ToLower(baseURL), "deepseek.com")
}

// upstreamUsesMaxTokens mirrors useMaxTokens in upstream detectCompat: these
// providers take max_tokens, and every other provider takes max_completion_tokens.
func upstreamUsesMaxTokens(provider, baseURL string) bool {
	switch provider {
	case "deepseek", "moonshotai", "moonshotai-cn", "cloudflare-ai-gateway", "together", "nvidia", "ant-ling", "zai", "zai-coding-cn":
		return true
	}
	return upstreamURLHas(baseURL, "chutes.ai", "api.moonshot.", "gateway.ai.cloudflare.com", "api.together.ai", "api.together.xyz", "integrate.api.nvidia.com", "api.ant-ling.com", "api.z.ai", "open.bigmodel.cn") ||
		strings.Contains(strings.ToLower(baseURL), "deepseek.com")
}

func upstreamURLHas(baseURL string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(baseURL, part) {
			return true
		}
	}
	return false
}
