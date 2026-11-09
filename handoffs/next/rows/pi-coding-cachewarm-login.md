# Ledger rows: fix/pi-coding-cachewarm-login (Pi 7bcc15c92, 683a7a868)

Upstream file -> disposition | Go target | covering tests | evidence

## 7bcc15c92 default cacheWarming to idle
- packages/coding-agent/src/core/settings-defaults.ts -> ported | internal/codingagent/settings.go (defaultCacheWarmingMode), internal/codingagent/configschema/settings.go (cacheWarming default) | settings_cache_warming_test.go, settings_manager_first_half_test.go, settings_manager_upstream_test.go, cache_warming_mode_union_test.go | red: default read "streaming"
- packages/coding-agent/schemas/settings.schema.json -> regenerated (make generate) | schemas/settings.schema.json | schema drift check |
- packages/coding-agent/docs/settings.md -> ported as PiG prose | docs/site/docs/settings.md, internal/pigdocs/content/settings.md (+ settings-examples.md, slash-commands.md `Mode: idle`) | docs-drift |
- packages/coding-agent/test/settings-manager.test.ts "cacheWarming" -> ported, same inputs with streaming/idle swapped as upstream | the three settings tests above |
- packages/coding-agent/CHANGELOG.md -> changelog.d/cwl-5b3661d297.md
- Parity scenarios test/parity/scenarios/slash-commands/{05,08}-*.toml: `Mode: streaming` -> `Mode: idle` (the /session Cache Warming block shows the default).
- No change to core/cache-warmer.ts port (internal/codingagent/cache_warmer.go): the 30-minute idle horizon already exists.

## 683a7a868 partner providers first in /login and /logout
- packages/coding-agent/src/core/preferred-providers.ts -> ported | ai/preferred_providers.go (PreferredProviderIDs, IsPreferredProvider, CompareProviders, ProviderRef; core/radius.ts lives in ai, so does this) | ai/preferred_providers_test.go (test/preferred-providers.test.ts)
- packages/coding-agent/src/modes/interactive/components/provider-brand.ts -> ported | tui/provider_brand.go (GetProviderBrandStyle, RadiusShimmer, openRouterGlow, StartBrandAnimation) | tui/oauth_selector_partner_test.go
- packages/coding-agent/src/modes/interactive/components/oauth-selector.ts -> ported | tui/oauth_selector.go (partner marker, animated selected row, Render/Dispose; the constructor's `tui` argument is NewOAuthSelectorComponentAnimated(requestRender func(), ...) because the renderer is replaceable) | tui/oauth_selector_partner_test.go (test/oauth-selector.test.ts: brand style only on selected row, accent without a TUI, star marks)
- packages/coding-agent/src/modes/interactive/components/radius-login-selector.ts -> ported (shimmer and timer moved to provider-brand) | internal/codingagent/radius_login_selector.go | login_radius_menu_test.go, radius_login_oracle_test.go (unchanged)
- packages/coding-agent/src/modes/interactive/interactive-mode.ts (getLoginProviderCompletionOptions, getLoginProviderOptions, getLogoutProviderOptions, selector construction with this.ui and dispose) -> ported | internal/codingagent/interactive_login.go (sortAuthProviders -> ai.CompareProviders), interactive_commands.go (SelectAuthProvider) | internal/codingagent/preferred_providers_order_test.go (login, logout, /login completions)
- packages/coding-agent/CHANGELOG.md -> changelog.d/cwl-5b3661d297.md

## c9552c7d8 coding-agent side (shared cache-warming policy)
- packages/ai/src/cache-warming.ts -> ported (cherry-pick -x of dp follow/p2-next 6b589db94, cleared in reviews/p2-next.md section 3; the one ai commit brought in) | ai/cache_warming.go | internal/codingagent/cache_warmer_test.go, cache_warmer_oracle_test.go, cache_warmer_format_oracle_test.go, cache_warming_mode_union_test.go
- packages/ai/src/index.ts `export * from "./cache-warming.ts"` -> the ai package exports the helpers (Go: package-level identifiers in ai/cache_warming.go)
- packages/coding-agent/src/core/cache-warmer.ts (-116 lines, uses the ai helpers) -> ported | internal/codingagent/cache_warmer.go (GetCacheWarmingDelayMs, GetPromptCacheTtlMs, IsReplayable removed; type aliases for CacheWarmingAction/Decision), coding/extension/cache_warming.go
- packages/coding-agent/test/cache-warmer.test.ts -> ported (imports move to ai; isReplayable -> isCacheWarmingReplayable) | internal/codingagent/cache_warmer_test.go
- packages/ai/CHANGELOG.md -> changelog.d/cwl-d696203940.md (first bullet of p2-next-6b589db94f.md; the p2 plugin bullet stays with follow/p2-next, so merging p2-next later repeats the ai bullet: drop one)
- The p2 plugin, p2 settings and p2 tests of c9552c7d8 are not in this lane (follow/p2-next).
