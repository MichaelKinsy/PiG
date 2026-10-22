// Package jsloop holds the ordering primitives that stand in for the one JavaScript thread Pi runs on. Pi's callbacks, RPC writes and extension handlers run on one event loop, so which of two things happens first is a language fact that Pi never writes down. PiG runs the same code on goroutines, threads and processes, and each place that touches the extension boundary has to state the order. Each type here names one of those orders; do not write another goroutine, mutex or channel pattern for it.
//
// extensions/sdk/internal/jsloop is a verbatim copy for the SDK module, which cannot import the root module. TestSDKCopy keeps the two identical.
package jsloop
