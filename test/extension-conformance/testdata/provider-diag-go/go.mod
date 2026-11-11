module provider-diag

go 1.27.0

require (
	github.com/MichaelKinsy/PiG v0.7.1-0.20261108053302-4e194449dcd7
	github.com/MichaelKinsy/PiG/extensions/sdk v0.7.1-0.20261108053302-4e194449dcd7
)

replace github.com/MichaelKinsy/PiG => ../../../..

replace github.com/MichaelKinsy/PiG/extensions/sdk => ../../../../extensions/sdk
