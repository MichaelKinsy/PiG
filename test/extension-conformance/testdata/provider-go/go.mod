module provider-producer

go 1.26.0

require (
	github.com/MichaelKinsy/PiG v0.3.1-0.20261022211800-1963ae7765f7
	github.com/MichaelKinsy/PiG/extensions/sdk v0.3.1-0.20261022211800-1963ae7765f7
)

replace github.com/MichaelKinsy/PiG => ../../../..

replace github.com/MichaelKinsy/PiG/extensions/sdk => ../../../../extensions/sdk
