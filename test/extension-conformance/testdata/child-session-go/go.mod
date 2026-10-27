module example.com/child-session-go

go 1.26.0

require (
	github.com/MichaelKinsy/PiG v0.3.1-0.20261026060701-a6e86b8979d5
	github.com/MichaelKinsy/PiG/extensions/sdk v0.3.1-0.20261026060701-a6e86b8979d5
)

replace github.com/MichaelKinsy/PiG => ../../../..

replace github.com/MichaelKinsy/PiG/extensions/sdk => ../../../../extensions/sdk
