module example.com/scenario-driver-go

go 1.27.0

require (
	github.com/MichaelKinsy/PiG v0.7.1-0.20261112135708-2daf79c20a25
	github.com/MichaelKinsy/PiG/extensions/sdk v0.7.1-0.20261112135708-2daf79c20a25
)

replace github.com/MichaelKinsy/PiG => ../../../..

replace github.com/MichaelKinsy/PiG/extensions/sdk => ../../../../extensions/sdk
