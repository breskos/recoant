package recoant

import "errors"

var (
	// ErrInvalidRef is returned for a reference without kind or ID.
	ErrInvalidRef = errors.New("recoant: invalid reference")
	// ErrInvalidSignal is returned for a signal that cannot be processed.
	ErrInvalidSignal = errors.New("recoant: invalid signal")
	// ErrInvalidRequest is returned for a request that cannot be answered.
	ErrInvalidRequest = errors.New("recoant: invalid request")
)
