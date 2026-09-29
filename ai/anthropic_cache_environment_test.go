package ai

import "testing"

// .upstream/v0.87.1/packages/ai/src/api/anthropic-messages.ts:60-84: only env=long selects a non-default retention; disabling requires the request option.
func TestAnthropicCacheRetentionEnvironmentDefaults(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "long")
	for _, row := range []struct {
		name   string
		option CacheRetention
		env    string
		want   CacheRetention
	}{
		{"empty scoped env falls back to ambient long", "", "", CacheRetentionLong},
		{"none env means short", "", "none", CacheRetentionShort},
		{"short env", "", "short", CacheRetentionShort},
		{"long env", "", "long", CacheRetentionLong},
		{"unknown env", "", "invalid", CacheRetentionShort},
		{"explicit none overrides long", CacheRetentionNone, "long", CacheRetentionNone},
		{"explicit short overrides long", CacheRetentionShort, "long", CacheRetentionShort},
		{"explicit long overrides none", CacheRetentionLong, "none", CacheRetentionLong},
	} {
		t.Run(row.name, func(t *testing.T) {
			if got := resolveAnthropicCacheRetention(row.option, ProviderEnv{"PI_CACHE_RETENTION": row.env}); got != row.want {
				t.Fatalf("retention=%q want=%q", got, row.want)
			}
		})
	}
	if got := resolveAnthropicCacheRetention("", nil); got != CacheRetentionLong {
		t.Fatalf("ambient retention=%q", got)
	}
}
