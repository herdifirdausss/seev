// Command notificationcheck is a fast, standalone pre-flight validator for
// Plan 59 (C3)'s NOTIFY_* environment contract. It intentionally does not
// call internal/platform/config.Load — that function validates the entire
// Gateway config (JWT, vendor, ledger, ...), so running it here would force
// this narrow check to also supply unrelated required env vars. The rules
// below mirror config.go's own NOTIFY_* validation; if that validation
// changes, update both.
package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getbool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func getint(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func main() {
	var errs []string

	env := getenv("APP_ENV", "development")
	emailEnabled := getbool("NOTIFY_EMAIL_ENABLED", false)
	pushEnabled := getbool("NOTIFY_PUSH_ENABLED", false)

	locale := getenv("NOTIFY_DEFAULT_LOCALE", "en-US")
	if locale != "en-US" && locale != "id-ID" {
		errs = append(errs, "NOTIFY_DEFAULT_LOCALE must be en-US or id-ID")
	}
	timezone := getenv("NOTIFY_DEFAULT_TIMEZONE", "Asia/Jakarta")
	if _, err := time.LoadLocation(timezone); err != nil {
		errs = append(errs, "NOTIFY_DEFAULT_TIMEZONE must be a valid IANA timezone: "+err.Error())
	}
	digestHour := getint("NOTIFY_DEFAULT_DIGEST_HOUR", 8)
	if digestHour < 0 || digestHour > 23 {
		errs = append(errs, "NOTIFY_DEFAULT_DIGEST_HOUR must be between 0 and 23")
	}
	maxDevices := getint("NOTIFY_MAX_DEVICES", 10)
	if maxDevices <= 0 || maxDevices > 100 {
		errs = append(errs, "NOTIFY_MAX_DEVICES must be between 1 and 100")
	}
	for _, sizing := range []struct {
		key      string
		fallback int
	}{
		{"NOTIFY_EVENT_PREFETCH", 10}, {"NOTIFY_EVENT_MAX_DELIVERY_ATTEMPTS", 5},
		{"NOTIFY_DELIVERY_BATCH_SIZE", 50}, {"NOTIFY_EMAIL_WORKERS", 2},
		{"NOTIFY_PUSH_WORKERS", 2}, {"NOTIFY_CONTACT_WORKERS", 2}, {"NOTIFY_DIGEST_WORKERS", 1},
	} {
		if getint(sizing.key, sizing.fallback) <= 0 {
			errs = append(errs, sizing.key+" must be positive")
		}
	}

	haveFingerprintKey := os.Getenv("NOTIFY_TOKEN_FINGERPRINT_KEY") != "" || os.Getenv("NOTIFY_TOKEN_FINGERPRINT_KEY_FILE") != ""
	if env == "production" && !haveFingerprintKey {
		errs = append(errs, "NOTIFY_TOKEN_FINGERPRINT_KEY or NOTIFY_TOKEN_FINGERPRINT_KEY_FILE is required in production")
	}

	if emailEnabled {
		if getenv("NOTIFY_SMTP_FROM", "") == "" {
			errs = append(errs, "NOTIFY_SMTP_FROM is required when NOTIFY_EMAIL_ENABLED=true")
		}
		host := getenv("NOTIFY_SMTP_HOST", "")
		port := getint("NOTIFY_SMTP_PORT", 25)
		if host == "" || port <= 0 || port > 65535 {
			errs = append(errs, "NOTIFY_SMTP_HOST and a valid NOTIFY_SMTP_PORT are required when NOTIFY_EMAIL_ENABLED=true")
		}
		mode := strings.ToLower(getenv("NOTIFY_SMTP_TLS_MODE", "starttls"))
		if mode != "starttls" && mode != "tls" && mode != "none" {
			errs = append(errs, "NOTIFY_SMTP_TLS_MODE must be starttls, tls, or none")
		}
		if mode == "none" && env == "production" {
			errs = append(errs, "NOTIFY_SMTP_TLS_MODE=none is not allowed in production")
		}
	}
	if pushEnabled {
		raw := getenv("NOTIFY_PUSH_PROVIDER_URL", "")
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			errs = append(errs, "NOTIFY_PUSH_PROVIDER_URL must be an absolute HTTP(S) URL when NOTIFY_PUSH_ENABLED=true")
		}
	}

	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "notificationcheck: NOTIFY_* configuration is invalid:")
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "  - "+e)
		}
		os.Exit(1)
	}
	fmt.Printf("notificationcheck: OK (env=%s email=%v push=%v locale=%s timezone=%s)\n", env, emailEnabled, pushEnabled, locale, timezone)
}
