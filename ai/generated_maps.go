package ai

// generatedPair is one key and value of a map of the generated catalog (models_generated.go).
type generatedPair[K comparable, V any] struct {
	key   K
	value V
}

// generatedMapRef says that generatedRows[row] has element of a table of maps.
type generatedMapRef struct{ row, element uint16 }

// generatedBuildMaps builds into maps the maps whose pairs are given, one for each of them. The pairs are laid out by the
// linker; a map literal is not, and costs code for every entry.
func generatedBuildMaps[K comparable, V any](maps []map[K]V, tables [][]generatedPair[K, V]) {
	for i, pairs := range tables {
		maps[i] = generatedMap(pairs)
	}
}

// generatedMap builds the map of pairs.
func generatedMap[K comparable, V any](pairs []generatedPair[K, V]) map[K]V {
	m := make(map[K]V, len(pairs))
	for _, pair := range pairs {
		m[pair.key] = pair.value
	}
	return m
}
