module example.com/benchext10/standalone/bench-ext-03

go 1.26

// Pig resolves the SDK requirement to the version-matched staged SDK at build time.
require (
	example.com/benchext10 v0.5.0
	github.com/MichaelKinsy/PiG/extensions/sdk v0.5.0
)

replace example.com/benchext10 => ../..
