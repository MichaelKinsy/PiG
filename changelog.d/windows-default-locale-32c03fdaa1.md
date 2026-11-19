### Fixed

- `FormatNumber`, `FormatTime` and `PlatformLocale` with no language use the Windows user's default locale (the name `GetLocaleInfoEx` gives, as ICU reads it, so as Node does) and no longer `LC_ALL`, `LC_MESSAGES` and `LANG` there; a Windows user with a non-English locale got English (en-US) numbers and times (D464). <!-- fix: 32c03fdaa1 --> <!-- cause: platformLocale was written for the POSIX branch of ICU's default-locale code only and had no Windows branch, so a Windows user, who has none of the variables, always got en-US. -->
