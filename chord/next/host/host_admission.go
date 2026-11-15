package host

import "github.com/MichaelKinsy/PiG/chord/next/data"

// Ports packages/chord/src/host/admission.ts.

// revoked is what a denial rejects the calls running for the denied caller with (slice 14 §2.2): `forbidden/revoked`,
// naming label and the denial's reason. The denied object itself shows `denial`, `forbidden` with the reason alone.
func revoked(label string, denial *data.ChordError) *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Reason: data.ReasonRevoked, Message: label + ": " + denial.Message}, nil)
}
