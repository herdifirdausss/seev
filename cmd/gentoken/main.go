// Command gentoken prints a signed JWT for local dev/test workflows — the
// same shape pkg/middleware.WithAuth verifies. Used by scripts/lib.sh
// (chaos-test.sh, smoke-test.sh) and safe to run standalone for manual
// curl-based testing, so nobody needs to hand-write a throwaway token
// generator again.
//
// Usage: JWT_SECRET=... gentoken <user-id> [role] [ttl] [kyc_level]
//
//	role defaults to "user"; ttl defaults to "1h" (Go duration syntax);
//	kyc_level defaults to 1 (docs/plan/39 Task T6, gotcha #9 master) — every
//	script using gentoken posts real gated transactions (transfer/topup/
//	payout), never exercises the KYC feature itself, so a minted token
//	should be transaction-capable by default. Real end-to-end proof of the
//	gate/tier journey uses actual register+submit+approve, never gentoken
//	(see scripts/business-e2e.sh's kyc_journey).
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/herdifirdausss/seev/pkg/middleware"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: JWT_SECRET=... gentoken <user-id> [role] [ttl]")
		os.Exit(2)
	}
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		fmt.Fprintln(os.Stderr, "gentoken: JWT_SECRET must be set in the environment")
		os.Exit(2)
	}

	userID := os.Args[1]
	role := "user"
	if len(os.Args) > 2 {
		role = os.Args[2]
	}
	ttl := time.Hour
	if len(os.Args) > 3 {
		parsed, err := time.ParseDuration(os.Args[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "gentoken: invalid ttl %q: %v\n", os.Args[3], err)
			os.Exit(2)
		}
		ttl = parsed
	}
	kycLevel := 1
	if len(os.Args) > 4 {
		parsed, err := strconv.Atoi(os.Args[4])
		if err != nil {
			fmt.Fprintf(os.Stderr, "gentoken: invalid kyc_level %q: %v\n", os.Args[4], err)
			os.Exit(2)
		}
		kycLevel = parsed
	}

	tok, err := middleware.GenerateToken(secret, middleware.Claims{
		UserID:   userID,
		Role:     role,
		KYCLevel: kycLevel,
		Exp:      time.Now().Add(ttl).Unix(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gentoken: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(tok)
}
