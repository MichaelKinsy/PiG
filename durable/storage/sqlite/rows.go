package sqlite

import "fmt"

// rowInt reads an INTEGER column.
func rowInt(row SqliteRow, column string) (int64, error) {
	switch value := row[column].(type) {
	case int64:
		return value, nil
	case int:
		return int64(value), nil
	case float64:
		if value == float64(int64(value)) {
			return int64(value), nil
		}
	}
	return 0, fmt.Errorf("SQLite column %s is not an integer: %v", column, row[column])
}

// rowText reads a TEXT column.
func rowText(row SqliteRow, column string) (string, error) {
	switch value := row[column].(type) {
	case string:
		return value, nil
	case []byte:
		return string(value), nil
	}
	return "", fmt.Errorf("SQLite column %s is not text: %v", column, row[column])
}
