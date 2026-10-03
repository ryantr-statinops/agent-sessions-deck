package events

import "errors"

// Errors returned by the package. They are wrapped with %w where context
// helps, so callers must classify them with errors.Is.
var (
	// ErrInvalidType reports an event with an empty event type.
	ErrInvalidType = errors.New("events: event type must not be empty")
	// ErrInvalidID reports a malformed event or session identifier, including
	// one that carries control bytes or is not valid UTF-8.
	ErrInvalidID = errors.New("events: identifier must be non-empty printable UTF-8")
	// ErrInvalidRevision reports a zero revision. Revisions start at 1.
	ErrInvalidRevision = errors.New("events: revision must be greater than zero")
	// ErrRevisionRegressed reports a revision that is not strictly greater
	// than the last revision the publisher published.
	ErrRevisionRegressed = errors.New("events: revision must be greater than the last published revision")
	// ErrPublisherClosed reports a publish against a closed publisher.
	ErrPublisherClosed = errors.New("events: publisher is closed")
	// ErrSubscriptionClosed reports a receive against a closed subscription.
	ErrSubscriptionClosed = errors.New("events: subscription is closed")
	// ErrInvalidBuffer reports a subscription buffer size below one.
	ErrInvalidBuffer = errors.New("events: subscription buffer must hold at least one notification")
	// ErrForbiddenMetadataKey reports a metadata field name that would carry
	// terminal bytes or environment/secret material.
	ErrForbiddenMetadataKey = errors.New("events: metadata field name may carry terminal bytes or secrets")
	// ErrInvalidMetadataKey reports an empty metadata field name.
	ErrInvalidMetadataKey = errors.New("events: metadata field name must not be empty")
	// ErrMetadataFieldLimit reports metadata that reached [MaxMetadataFields].
	ErrMetadataFieldLimit = errors.New("events: metadata field limit reached")
	// ErrMetadataValueLimit reports a metadata value beyond the length caps.
	ErrMetadataValueLimit = errors.New("events: metadata value exceeds the length limit")
	// ErrInvalidMetadataValue reports a zero Value, which carries no kind.
	ErrInvalidMetadataValue = errors.New("events: metadata value kind is invalid")
	// ErrForbiddenMetadataValue reports a metadata value that is not printable
	// UTF-8 text, which is how raw terminal bytes are refused.
	ErrForbiddenMetadataValue = errors.New("events: metadata value may carry raw terminal bytes")
)
