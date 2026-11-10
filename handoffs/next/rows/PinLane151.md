# PinLane151 rows (pin bump to Pi v1.5.1, dd30101cf)

## Deferred: Pi 03d97fea (apps/p2)
apps/p2 is not a tracked source root (test/parity/upstreampackages), the pin tree has no apps/, and PiG's p2 lives on the follow/p2 lineage with no PORT_MAP rows. The post-pin p2 commit (about 1.2k lines) is deferred to FollowP2Next; no upstream-sync manifest row exists for it.

## docs 6a170394, 9abcd30f
CHANGELOG-only in Pi; the vendored shim CHANGELOG regenerates via vendor-pi-dist. No docs/ changes in Pi.

## SDK export ledger
pi-ai cache-warming exports (CACHE_WARMING_* constants, evaluateCacheWarming, getCacheWarmingDelayMs, getCacheWarmingRequestOptions, getPromptCacheTtlMs, isCacheWarmingReplayable; root and compat) are gap:SdkPiAiHelpers (pure pi-ai helpers); OAuthSelectorComponent.dispose is gap:SdkExportsLedger like the rest of that class.
