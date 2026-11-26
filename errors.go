package recoant

import "errors"

var (
	// ErrInvalidRef is returned for a reference without kind or ID.
	ErrInvalidRef = errors.New("recoant: invalid reference")
	// ErrInvalidSignal is returned for a signal that cannot be processed.
	ErrInvalidSignal = errors.New("recoant: invalid signal")
	// ErrInvalidRequest is returned for a request that cannot be answered.
	ErrInvalidRequest = errors.New("recoant: invalid request")
	// ErrUnsupported is returned for a request direction a recommender
	// cannot answer, e.g. user-to-user from an item-based model.
	ErrUnsupported = errors.New("recoant: unsupported request direction")
	// ErrUnfiltered is returned by recommenders for a request with
	// attribute conditions that no attribute store evaluated; see
	// [Request.Where].
	ErrUnfiltered = errors.New("recoant: attribute conditions need an attribute store (attr.Filtered)")
)
