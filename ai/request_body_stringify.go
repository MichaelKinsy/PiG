package ai

// replacementCharacter is U+FFFD as encoding/json writes it for an ill-formed byte. A request body that holds it is encoded again by the
// JSON.stringify encoder, which writes a lone UTF-16 surrogate as its escape (jsstring.MarshalKeepingSurrogates states the rule).
var replacementCharacter = []byte("\uFFFD")
