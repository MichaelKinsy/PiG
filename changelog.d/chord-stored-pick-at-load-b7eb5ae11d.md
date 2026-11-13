### Changed

- Chord pages: the language and the theme a user chose earlier are applied when a page opens or reloads. Pi's followers apply a stored pick only at its next change, so the choice looked unsaved after a reload; PiG's apply the value their watch of the pick was acquired with (divergence D636: a patch over Pi's two vendored followers, and the Go ports `FollowActiveLanguage` and `FollowActiveTheme`).
