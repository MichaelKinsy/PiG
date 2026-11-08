package store

// ThrownError is a value a callback panicked with, as the error a commit it rejected carries.
type ThrownError struct{ Value any }

func (err *ThrownError) Error() string { return "" }
