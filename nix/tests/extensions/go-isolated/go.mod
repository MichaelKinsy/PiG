module example.com/goisolated

go 1.26

// A release no installation has: the installation's go builds this module itself instead of switching to it.
toolchain go1.99.0

require github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0
