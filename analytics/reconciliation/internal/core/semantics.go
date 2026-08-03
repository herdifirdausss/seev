package core

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Delta is deliberately integer-only. C2 never rounds or converts financial
// values while comparing source and warehouse facts.
func Delta(expected, actual int64) int64 { return actual - expected }

func SeverityFor(delta int64, criticalOnMismatch bool) Severity {
	if delta == 0 {
		return SeverityInfo
	}
	if criticalOnMismatch {
		return SeverityCritical
	}
	return SeverityWarning
}

func SignedEntry(direction string, amountMinor int64) int64 {
	if strings.EqualFold(direction, "debit") {
		return -amountMinor
	}
	return amountMinor
}

func Balanced(debitMinor, creditMinor int64) bool { return debitMinor == creditMinor }

// SafeCutoff is the latest instant that every input has observed. A cutoff is
// the minimum rather than the maximum so in-flight CDC cannot create a false
// source-to-warehouse mismatch.
func SafeCutoff(cutoffs ...time.Time) time.Time {
	if len(cutoffs) == 0 {
		return time.Time{}
	}
	cutoff := cutoffs[0]
	for _, candidate := range cutoffs[1:] {
		if candidate.Before(cutoff) {
			cutoff = candidate
		}
	}
	return cutoff.UTC()
}

var sensitiveDetail = regexp.MustCompile(`(?i)(password|token|secret|authorization|credential|private[_-]?key|access[_-]?key|refresh[_-]?token|session|cookie|raw[_-]?(payload|request|response)|destination|account[_-]?number|document|kyc|user[_-]?id|customer[_-]?id)`)

// RedactDetails keeps control-plane evidence useful without allowing source
// row values or sensitive field names to become an incident side channel.
func RedactDetails(details string) string {
	if sensitiveDetail.MatchString(details) {
		return "redacted sensitive detail"
	}
	return details
}

func Pseudonym(salt []byte, value string) string {
	mac := hmac.New(sha256.New, salt)
	_, _ = mac.Write([]byte(value))
	return "pseudonym_" + hex.EncodeToString(mac.Sum(nil))
}
