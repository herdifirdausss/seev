# Payout use cases

This domain-named package owns Payout workflow and state-transition decisions.
Start with `payout.go` and `orchestrate.go`, then follow `routing.go`,
`failover*`, `relay.go`, `intake.go`, and `merchant.go` for each capability.

Persistence, transports, vendor dispatch workers, and cross-service contracts
stay in their explicit sibling boundaries. Payout does not import Payin or
VendorService implementation code.
