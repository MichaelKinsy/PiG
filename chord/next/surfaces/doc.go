// Package surfaces ports the portable part of packages/chord/src/surfaces (Pi main c23b9ec1, slice 11): messages and
// languages in the app kit (decision "Messages and languages in the app kit"), the `Translations` registry, the user's
// locale preference and the language a client follows.
//
// Forced Go differences from upstream, each also noted on the identifier (language mechanics):
//   - Pi's render scope (`inLanguage`, `currentLanguage`) is a module-wide variable a synchronous render sets; Go's is
//     the context a render is given: InLanguage runs a render with a language current in its context, CurrentLanguage
//     reads it, Messages.InScope is Pi's `t` outside `in`. A message function receives its language as its first
//     parameter (nil outside a render) and passes it on (`FormatNumber(lang, ...)`, `other.In(lang).T(...)`).
//   - The type-level checks of defineMessages (every catalog matches the first, `DataKeys`) are run-time checks in Go:
//     DefineMessages refuses a catalog whose keys or text/function kinds differ from the first's, and Message panics on a
//     key the first lacks or an argument that is no plain data. The typed `t` signatures have no Go form; a call with a
//     key the first catalog lacks is the error a JavaScript call through the same function throws.
//   - A message value is a string or a MessageFunc; a `throw` is a returned error (a panic is recovered as one).
package surfaces
