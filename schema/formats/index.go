// Ports packages/schema/src/formats/index.ts

// Package formats holds the format checks of JSON Schema 2020-12, for the schema package's formats option. An optional entry point: schemas compiled without it treat `format` as an annotation and do not link these checks. The checks are adapted from TypeBox 1.3.27 (MIT).
package formats

import "github.com/MichaelKinsy/PiG/schema"

// Formats is Pi's `formats` export: every format JSON Schema 2020-12 defines, by name, plus `url` and `json-pointer-uri-fragment`, which TypeBox and ajv-formats also check. It returns a new map on each call.
func Formats() map[string]schema.FormatCheck {
	return map[string]schema.FormatCheck{
		"date":                      IsDate,
		"date-time":                 IsDateTime,
		"duration":                  IsDuration,
		"email":                     IsEmail,
		"hostname":                  IsHostname,
		"idn-email":                 IsIdnEmail,
		"idn-hostname":              IsIdnHostname,
		"ipv4":                      IsIPv4,
		"ipv6":                      IsIPv6,
		"iri":                       IsIri,
		"iri-reference":             IsIriReference,
		"json-pointer":              IsJsonPointer,
		"json-pointer-uri-fragment": IsJsonPointerUriFragment,
		"regex":                     IsRegex,
		"relative-json-pointer":     IsRelativeJsonPointer,
		"time":                      IsTime,
		"uri":                       IsUri,
		"uri-reference":             IsUriReference,
		"uri-template":              IsUriTemplate,
		"url":                       IsUrl,
		"uuid":                      IsUuid,
	}
}
