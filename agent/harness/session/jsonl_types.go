package session

import "github.com/MichaelKinsy/PiG/agent/harness"

// JSONLFormatVersion and JSONLStorageVersion belong to the upstream file format.
const (
	JSONLFormatVersion  = 4
	JSONLStorageVersion = 1
)

// JsonlStorageHeader is the upstream format-4 file header.
type JsonlStorageHeader struct {
	V                       int    `json:"v"`
	Kind                    string `json:"kind"`
	ID                      string `json:"id"`
	StorageVersion          int    `json:"storageVersion"`
	CreatedAt               int64  `json:"createdAt"`
	Cwd                     string `json:"cwd"`
	ParentSessionID         string `json:"parentSessionId,omitempty"`
	LegacyParentSessionPath string `json:"legacyParentSessionPath,omitempty"`
	NextSeq                 *int64 `json:"nextSeq,omitempty"`
}

// JsonlStorageOptions supplies the filesystem and destination path.
type JsonlStorageOptions struct {
	FileSystem harness.FileSystem
	Path       string
	Now        func() int64
}

// JsonlSessionRepoOptions configures the file-backed repository.
type JsonlSessionRepoOptions struct {
	FileSystem   harness.FileSystem
	SessionsRoot string
	Now          func() int64
}

// JsonlSessionListOptions optionally restricts discovery to a working directory.
type JsonlSessionListOptions struct{ Cwd *string }

// LegacyV3SessionHeader is Pi's upstream format-3 header.
type LegacyV3SessionHeader struct {
	Type          string  `json:"type"`
	Version       int     `json:"version"`
	ID            string  `json:"id"`
	Timestamp     string  `json:"timestamp"`
	Cwd           string  `json:"cwd"`
	ParentSession *string `json:"parentSession,omitempty"`
}

// JsonlParsedSessionHeader discriminates upstream file formats.
type JsonlParsedSessionHeader struct {
	Header *JsonlStorageHeader
	Legacy *LegacyV3SessionHeader
}
