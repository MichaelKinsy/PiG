module example.com/scenario-driver-go

go 1.26.0

require (
	github.com/MichaelKinsy/PiG v0.5.3
	github.com/MichaelKinsy/PiG/extensions/sdk v0.5.3
)

replace github.com/MichaelKinsy/PiG => ../../../..

replace github.com/MichaelKinsy/PiG/extensions/sdk => ../../../../extensions/sdk
