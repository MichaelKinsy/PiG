module provider-producer

go 1.26.0

require (
	github.com/MichaelKinsy/PiG v0.4.0
	github.com/MichaelKinsy/PiG/extensions/sdk v0.4.0
)

replace github.com/MichaelKinsy/PiG => ../../../..

replace github.com/MichaelKinsy/PiG/extensions/sdk => ../../../../extensions/sdk
