// Package bedrock is the Amazon Bedrock Converse Stream provider (bedrock-converse-stream). It lives outside ai so that a program that imports ai alone does not link the AWS SDK; importing this package, or ai/all, registers the provider from init with ai.RegisterOptionalAPI.
//
// pig additive (D92): a Piglet Binary built with the pig_strip_bedrock_converse_stream tag compiles every other file of this package out, so the AWS SDK is not linked there either, and the API stays unregistered and stripped.
package bedrock
