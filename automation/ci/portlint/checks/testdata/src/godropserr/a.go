package godropserr

func work() error { return nil }

func bad() {
	go work() // want `go statement discards the call's error result`
}

func good() {
	go func() { _ = work() }()
}
