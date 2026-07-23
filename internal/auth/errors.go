package auth

import "errors"

// ErrEmailTaken maps to 409 — the email is already registered
// (case-insensitive).
var ErrEmailTaken = errors.New("auth: email already registered")

// ErrInvalidCredentials maps to 401 — deliberately covers BOTH "email not
// found" and "wrong password" so a login probe can't distinguish which one
// happened (no account-existence oracle).
var ErrInvalidCredentials = errors.New("auth: invalid email or password")

// ErrUserDisabled maps to 403 — the identity exists and the password was
// right, but the account is administratively disabled.
var ErrUserDisabled = errors.New("auth: account disabled")

// ErrInvalidRefreshToken maps to 401 — unknown, expired, or revoked refresh
// token. A REVOKED-token presentation additionally revokes the user's whole
// chain before this is returned (replay containment, docs/roadmap/archive/25 T1).
var ErrInvalidRefreshToken = errors.New("auth: invalid refresh token")

// ErrValidation maps to 400 — malformed input (bad email, short password).
var ErrValidation = errors.New("auth: validation failed")

var ErrKYCLevelSequence = errors.New("auth: kyc level must be the next level")
var ErrKYCPending = errors.New("auth: a kyc submission is already pending")
var ErrKYCProvider = errors.New("auth: kyc provider failed")
