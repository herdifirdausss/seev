package vendorgw

// Registry is a plain, explicitly-populated lookup of vendor name →
// verifier. It deliberately has NO knowledge of which vendor
// implementations exist — vendorgw itself never imports a concrete vendor
// subpackage (e.g. mockvendor), because that subpackage in turn imports
// vendorgw for the PayinEvent/PayinVerifier types, and a two-way import
// would be a compile-time cycle. The composition root (cmd/gateway/main.go)
// constructs each enabled vendor's concrete verifier and registers it here
// — the same explicit-construction-based-on-config idiom already used
// there for Redis-vs-in-memory fallbacks (docs/plan/12 Task T1). Adding a
// real vendor later is one new subpackage plus one registration line in
// main.go; internal/payin's code never changes (docs/plan/21 K-T6).
type Registry struct {
	payin  map[string]PayinVerifier
	payout map[string]PayoutProvider
}

func NewRegistry() *Registry {
	return &Registry{payin: make(map[string]PayinVerifier), payout: make(map[string]PayoutProvider)}
}

// AddPayin registers v under its own Vendor() name. Call this only for
// vendors actually enabled by config — an unregistered vendor name makes
// Payin return false, which the webhook receiver maps to 404 (docs/plan/22
// Task T3), not a 500.
func (r *Registry) AddPayin(v PayinVerifier) {
	r.payin[v.Vendor()] = v
}

// Payin looks up a registered payin verifier by vendor name.
func (r *Registry) Payin(vendor string) (PayinVerifier, bool) {
	v, ok := r.payin[vendor]
	return v, ok
}

// AddPayout registers v under its own Vendor() name (docs/plan/23 Task T2).
func (r *Registry) AddPayout(v PayoutProvider) {
	r.payout[v.Vendor()] = v
}

// Payout looks up a registered payout provider by vendor name.
func (r *Registry) Payout(vendor string) (PayoutProvider, bool) {
	v, ok := r.payout[vendor]
	return v, ok
}
