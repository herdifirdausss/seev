package cryptox

import "strings"

// MaskEmail implements K2's "masked ... audit identity" requirement for
// fields that must never be reversible (unlike Ring/LookupKey, there is no
// way back to the original value from a masked one — that's deliberate:
// an audit trail is read by more people, for longer, than the row it
// describes, so it gets a weaker, one-way exposure instead of ciphertext).
//
// Deterministic: the same input always produces the same output, so an
// exact-match lookup against already-masked rows works by masking the
// search term the same way (see services/adminbff/internal/admin/audit.go's own
// ListAudit).
func MaskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return "***"
	}
	local, domain := email[:at], email[at+1:]
	masked := string(local[0]) + "***@"
	if dot := strings.LastIndex(domain, "."); dot > 0 {
		masked += string(domain[0]) + "***" + domain[dot:]
	} else {
		masked += string(domain[0]) + "***"
	}
	return masked
}
