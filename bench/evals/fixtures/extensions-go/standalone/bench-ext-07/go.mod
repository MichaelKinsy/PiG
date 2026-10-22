module example.com/benchext10/standalone/bench-ext-07

go 1.26

// Pig resolves the SDK requirement to the version-matched staged SDK at build time.
require (
	example.com/benchext10 v0.5.2
	github.com/MichaelKinsy/PiG/extensions/sdk v0.3.1-0.20261022211800-1963ae7765f7
)

replace example.com/benchext10 => ../..
