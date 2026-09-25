module example.com/plan-mode

go 1.26

require (
	github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0
	golang.org/x/text v0.41.0
)

replace github.com/MichaelKinsy/PiG/extensions/sdk => ../../../extensions/sdk
