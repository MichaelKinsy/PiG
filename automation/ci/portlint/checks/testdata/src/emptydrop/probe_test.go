package emptydrop

// A test's probe input is not a Pi wire shape, so omitempty on it is not reported.
type probeInput struct {
	Bindings map[string][]string `json:"bindings,omitempty"`
}
