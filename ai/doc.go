// Package ai provides provider abstractions, model definitions, and
// auth storage for pig.
//
// The model catalogs are generated from the exact published Pi package pinned
// by extensions/sdk-ts. Run `make model-catalogs` after changing the upstream
// pin.
package ai

//go:generate ../automation/gen/generate-model-catalogs.sh
