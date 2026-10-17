package source

// SourceInfo mirrors core/source-info.ts SourceInfo: where a resource, tool or command came from.
type SourceInfo struct {
	Path    string `json:"path"`
	Source  string `json:"source"`
	Scope   string `json:"scope"`
	Origin  string `json:"origin"`
	BaseDir string `json:"baseDir,omitempty"`
}
