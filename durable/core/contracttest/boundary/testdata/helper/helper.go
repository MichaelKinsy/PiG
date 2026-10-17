// Package helper hides text/template behind a module-internal import: the transitive violation of viahelper.
package helper

import "text/template"

func Parse(text string) (*template.Template, error) { return template.New("t").Parse(text) }
