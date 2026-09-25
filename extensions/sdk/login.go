package sdk

// pig additive (D60): Go extensions provide typed data for Pig's native
// login template instead of Pi's in-process TUI component factory.
// LoginDefinition describes one login using Pig's fixed native template.
// Grid cells are printable ASCII palette symbols; '.' is transparent.
type LoginDefinition struct {
	Brand       []string          `json:"brand"`
	Hero        []string          `json:"hero"`
	Mascot      []string          `json:"mascot"`
	Palette     map[string]string `json:"palette"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Tagline     string            `json:"tagline"`
}
