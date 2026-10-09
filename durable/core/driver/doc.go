// SPDX-License-Identifier: MIT

// Package driver holds the native Go effect executors of the Durable core (docs/plan/durable-core/ABI.md section 6, ADR-0001
// D17). A sqlhost.Host starts them in owned goroutines under a context.Context; they post completions back to the Host's
// owner goroutine. The executors serve the model plane: model_context and deferred over a durable.Models, the same calls
// pi-ai's Models makes in the Cloudflare host (durable/core/host/cf/src/effects.ts).
package driver
