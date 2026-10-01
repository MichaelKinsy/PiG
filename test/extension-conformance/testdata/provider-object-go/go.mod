module example.com/provider-object-go

go 1.26.0

require (
	github.com/MichaelKinsy/PiG v0.3.1
	github.com/MichaelKinsy/PiG/extensions/sdk v0.3.1
)

replace github.com/MichaelKinsy/PiG => ../../../..

replace github.com/MichaelKinsy/PiG/extensions/sdk => ../../../../extensions/sdk
