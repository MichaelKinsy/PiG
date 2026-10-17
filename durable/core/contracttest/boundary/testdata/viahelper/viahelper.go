// Package viahelper is a deliberate transitive violation: it reaches text/template through helper.
package viahelper

import "github.com/MichaelKinsy/PiG/durable/core/contracttest/boundary/testdata/helper"

func Render() error { _, err := helper.Parse("x"); return err }
