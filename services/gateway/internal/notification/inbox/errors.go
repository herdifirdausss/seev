package notify

import "errors"

// ErrNotificationNotFound means no notification exists for the given id
// (docs/roadmap/archive/25 Task T4), or it isn't owned by the requesting user — the
// HTTP layer maps this to 404 either way, never confirming existence to a
// non-owner.
var ErrNotificationNotFound = errors.New("notify: notification not found")
