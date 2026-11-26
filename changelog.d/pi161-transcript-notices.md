### Breaking Changes

- Replaced the `showCacheMissNotices` setting with per-notice toggles (`notices.cacheMisses`, `notices.cacheWarming`, `notices.compactionCosts`, `notices.reasoningDrops`) under Notices in `/settings`. Existing `showCacheMissNotices` values are migrated to all four keys. `SettingsManager.GetShowCacheMissNotices` and `SettingsManager.SetShowCacheMissNotices` were removed; use `SettingsManager.GetNotices` and `SettingsManager.SetNotice` instead.
