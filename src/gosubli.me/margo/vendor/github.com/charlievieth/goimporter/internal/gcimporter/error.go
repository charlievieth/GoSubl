package gcimporter

// A NotFoundError is returned by Import when the package file cannot be found.
type NotFoundError struct {
	id   string
	path string
}

func (e *NotFoundError) Error() string {
	return "cannot find import: " + e.id
}

// String returns e formatted as 'NotFoundError{id: %s path: %s}' for debugging.
func (e *NotFoundError) String() string {
	return "NotFoundError{id: " + e.id + " path: " + e.path + "}"
}

// ID returns the package id created by FinkPkg for the missing package.
func (e *NotFoundError) ID() string {
	return e.id
}

// Path returns the path argument passed to Import and FindPkg.
func (e *NotFoundError) Path() string {
	return e.path
}

// IsNotFound returns if err is a NotFoundError.
func IsNotFound(err error) bool {
	_, ok := err.(*NotFoundError)
	return ok
}
