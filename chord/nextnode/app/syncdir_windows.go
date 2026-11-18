package app

// flushDirectory does nothing on Windows: a directory cannot be opened for FlushFileBuffers there (access denied), and the entries are as
// durable as the file system makes them. It is here and not a call of PiG's file-write package because Chord may not link that package (the
// boundary test chord/boundary_upstream_test.go lists what Chord may link).
func flushDirectory(string) error { return nil }
