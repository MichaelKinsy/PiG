module example.com/plan-mode

go 1.26

require (
	github.com/MichaelKinsy/PiG/extensions/sdk v0.7.1-0.20261112135708-2daf79c20a25
	golang.org/x/text v0.41.0
)

replace github.com/MichaelKinsy/PiG/extensions/sdk => ../../../extensions/sdk
