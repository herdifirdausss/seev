package repository

import "errors"

// ErrNotFound is returned when no row matches the lookup.
var ErrNotFound = errors.New("auth: not found")

// ErrDuplicateEmail is returned by CreateUser when the (case-insensitive)
// email is already registered — backed by idx_auth_users_email, never a
// read-then-write race.
var ErrDuplicateEmail = errors.New("auth: email already registered")
var ErrKYCSubmissionNotFound = errors.New("auth: kyc submission not found")
var ErrKYCSubmissionNotPending = errors.New("auth: kyc submission is not pending")
var ErrKYCTierConflict = errors.New("auth: kyc tier conflict")
var ErrKYCApplyTier = errors.New("auth: apply kyc tier failed")
