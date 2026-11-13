### Changed

- After this upgrade every browser and every paired device of an app built with the Chord app kit signs in again. The kit's sessions are now sign-ins (Pi ff7414d51, "Sign-ins, not sessions"), under new names with no alias and no migration, as in Pi: nothing stored or held under the old names is read. The old stored document stays in the server's store untouched; a browser with the old cookie gets 401 (no redirect: open the owner link the start prints); a paired device's token gets 401 (pair it again); the owner token file is replaced at the next start, so command-line clients on the same machine keep working.
- Chord app kit, renamed on the wire and in the store: the contract and core facet `chord.app.sessions` -> `chord.app.sign-ins`; the stored document `chord.app.sessions` with the field `sessions` -> `chord.app.sign-ins` with the field `signIns`; the cookie `chord-session` -> `chord-sign-in`.
- Chord app kit, renamed start option: `AppLimits.EphemeralSessions` -> `AppLimits.EphemeralSignIns` (Pi `limits.ephemeralSessions` -> `limits.ephemeralSignIns`).
- Chord app kit, renamed: `app.Sessions` -> `app.SignIns`.
- Chord app kit, renamed: `app.SessionInfo` -> `app.SignInInfo`.
- Chord app kit, renamed: `app.SessionKind` -> `app.SignInKind`, with `SessionBrowser`, `SessionDevice`, `SessionEphemeral` -> `SignInBrowser`, `SignInDevice`, `SignInEphemeral`.
- Chord app kit, renamed: `app.SessionCookie` -> `app.SignInCookie`.
- Chord app kit, renamed: `app.SessionLimits` -> `app.SignInLimits`.
- Chord app kit, renamed: `app.KitSessions` -> `app.KitSignIns`.
- Chord app kit, renamed: `app.CreateSessions` -> `app.CreateSignIns`.
- Chord app kit, renamed: `app.EphemeralSessions` -> `app.EphemeralSignIns`, `app.NewEphemeralSessions` -> `app.NewEphemeralSignIns`.
