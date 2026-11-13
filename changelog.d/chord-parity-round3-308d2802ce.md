### Fixed

- `chord/next/surfaces`: a locale tag whose `-u-` extension is only `true` or `yes` keeps it as Node's `Intl.getCanonicalLocales` does (`en-u-true` and `en-u-yes` are attributes); the lone `true` or `yes` of another singleton is still dropped. `TestLocaleTablesMatchIntl` covers both tags. <!-- fix: 308d2802ce -->
- `chord/nextnode/plugins`: a Go facet that reloads twice in a row no longer leaves the copy of its keyed member on the slot of the first reload (the mirror stopped resyncing after it queued the reset, so an end of the next slot in between was lost), and a method called while the copy is read again answers after the frames of its commits were sent, so the plugin reads what it wrote. <!-- fix: 308d2802ce -->
