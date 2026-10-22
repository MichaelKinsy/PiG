module example.com/plan-mode

go 1.26

require (
	github.com/MichaelKinsy/PiG/extensions/sdk v0.3.1-0.20261022211800-1963ae7765f7
	golang.org/x/text v0.41.0
)

replace github.com/MichaelKinsy/PiG/extensions/sdk => ../../../extensions/sdk
