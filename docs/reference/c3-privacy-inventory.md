# C3 Privacy Inventory

| Data | Storage | Access/retention treatment |
|---|---|---|
| Notification copy and typed context | Gateway `notif_notifications` | user-scoped reads; raw source payload is `{}` for new rows; bounded retention |
| Email recipient | `notif_deliveries.recipient_ciphertext` | cryptox envelope, key version and HMAC fingerprint; decrypted only immediately before SMTP; redacted after the short operational window |
| Push token | `notif_device_endpoints.token_ciphertext` | cryptox envelope; public API exposes only suffix/status; revoked/invalid ciphertext is redacted |
| Delivery evidence | `notif_deliveries`, `notif_delivery_attempts` | status, hashes, bounded codes/excerpts; no recipient plaintext; bounded retention |
| Preferences/settings | `notif_preferences`, `notif_user_settings` | closure deletes current subject state; export returns sanitized configuration |
| Digest membership | `notif_digest_windows`, `notif_digest_items` | user-scoped and bounded; export includes window metadata, not rendered provider content |

Account closure deletes settings, preferences, and device endpoints, then
pseudonymizes retained notification/delivery/digest ownership with a stable
surrogate. User export is paginated and omits raw tokens, ciphertext, provider
payloads, rendered HTML, and email addresses.
