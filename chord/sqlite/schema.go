package sqlite

// Ports packages/chord/src/sqlite/schema.ts

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

var sqliteSchemaPrefixPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateSqliteSchemaPrefix checks a prefix before it becomes part of a SQLite identifier: it must be an identifier and
// must not begin with SQLite's reserved `sqlite_` (any case).
func ValidateSqliteSchemaPrefix(prefix string) error {
	if !sqliteSchemaPrefixPattern.MatchString(prefix) {
		return fmt.Errorf("Invalid SQLite schema prefix %s", jsonQuote(prefix))
	}
	if strings.HasPrefix(strings.ToLower(prefix), "sqlite_") {
		return fmt.Errorf("SQLite schema prefix %s is reserved", jsonQuote(prefix))
	}
	return nil
}

// jsonQuote is JSON.stringify(text) for an error message.
func jsonQuote(text string) string {
	quoted, err := jsstring.MarshalJSON(text)
	if err != nil {
		return strconv.Quote(text)
	}
	return string(quoted)
}
