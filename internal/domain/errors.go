// Package domain contains shared, infrastructure-independent domain primitives.
package domain

import "errors"

// ErrNotImplemented marks an unavailable operation. It is never a successful
// verification result or permission decision.
var ErrNotImplemented = errors.New("operation is not implemented yet")
