// Package json stands in for the shared host/Go-SDK codec, a fork of encoding/json with the same API.
package json

import "io"

func Marshal(v any) ([]byte, error) { return nil, nil }

func MarshalIndent(v any, prefix, indent string) ([]byte, error) { return nil, nil }

type Encoder struct{ w io.Writer }

func NewEncoder(w io.Writer) *Encoder { return &Encoder{w: w} }

func (e *Encoder) Encode(v any) error { return nil }

func (e *Encoder) SetEscapeHTML(on bool) {}
