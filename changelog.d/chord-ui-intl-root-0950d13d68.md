### Fixed

- Chord: `FormatNumber` and `FormatTime` format in ICU's root locale when neither the tag nor the platform locale has data, as Node does (a `LANG=xx_YY` prints `14:05`, `xx_US` `2:05 PM`, a variable set to the empty string is the locale `und`), and apply CLDR's compound legacy aliases (`hy-arevmda` is `hyw`, `zh-hakka` is `hak`, `zh-xiang` is `hsn`) before the lookup. <!-- fix: 0950d13d68 -->
