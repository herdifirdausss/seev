# Payin use cases

This domain-named package owns Payin workflow decisions. Use `topup.go`,
`routing*.go`, `merchant.go`, and `intake.go` as the capability map; privacy
and retention are lifecycle concerns kept in their named files.

Repositories, transports, vendor contracts, and workers remain separate
boundaries. Payin does not import Payout or VendorService implementation code.
