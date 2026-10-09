package ai

// StrippableAPIs lists the APIs a Piglet can strip (strip.apis). A Piglet
// Binary compiles each one's implementation out with the build tag
// pig_strip_<api> (see internal/pigstrip.Tag), and a Piglet that Stock PiG
// runs disables it at runtime. A stripped API's models are not offered
// (OfferedModels), and building a provider for one of them fails.
// pig additive (D92): Stock PiG strips no API.
func StrippableAPIs() []API {
	return []API{APIBedrockConverseStream, APIGoogleVertex, APIMistralConversations}
}
