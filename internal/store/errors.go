package store

import "errors"

// ErrInvalid reports an argument Open cannot honour, such as a nil database.
var ErrInvalid = errors.New("store: invalid config")
