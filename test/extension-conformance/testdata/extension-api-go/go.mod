module example.com/extension-api-go

go 1.27.0

require (
	github.com/MichaelKinsy/PiG v0.7.1-0.20261115110416-4c9c52e1bf09
	github.com/MichaelKinsy/PiG/extensions/sdk v0.7.1-0.20261115110416-4c9c52e1bf09
)

replace github.com/MichaelKinsy/PiG => ../../../..

replace github.com/MichaelKinsy/PiG/extensions/sdk => ../../../../extensions/sdk
