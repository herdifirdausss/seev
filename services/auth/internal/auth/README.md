# Auth use cases

This domain-named package owns Auth decisions. The public facade in
`services/auth/module.go` composes it; it is not a generic application bucket.

| Capability | Files |
|---|---|
| Identity and token lifecycle | `auth.go`, `bootstrap.go`, `errors.go` |
| KYC and provider decisions | `kyc.go`, `kyc_retry.go` |
| Documents | `documents.go`, `file_document_store.go` |
| Privacy and closure | `privacy*.go`, `closure*.go`, `owner_closure_client.go` |
| Object deletion | `object_outbox.go` |
| Operator lifecycle | `operator_offboarding.go` |
| Retention and observability | `retention.go`, `metrics*.go`, `privacy_worker.go` |

Persistence ports stay in `../repository/`; KYC outbound adapters stay in
`../adapter/kycvendor/`; HTTP and background adapters stay in their explicit
transport and worker packages. Split a capability into its own package only
when its invariants and visibility can be preserved by a focused change.
