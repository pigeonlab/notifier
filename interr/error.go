package interr

import "errors"

// ErrRequestsNotFound is fired when a request is not provided.
var ErrRequestsNotFound = errors.New("no requests provided")

// ErrIgnored is fired when a request has been ignored.
var ErrIgnored = errors.New("request ignored")
