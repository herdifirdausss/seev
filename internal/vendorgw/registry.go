package vendorgw

// Registry is a plain, explicitly-populated lookup of vendor name →
// verifier/provider. It deliberately has NO knowledge of which vendor
// implementations exist — vendorgw itself never imports a concrete vendor
// subpackage (e.g. mockvendor), because that subpackage in turn imports
// vendorgw for the adapter types, and a two-way import would be a compile-time
// cycle. Payin registers only a routing marker; VendorService composes the
// concrete callback verifier and outbound adapter.
type Registry struct {
	payin  map[string]PayinVendor
	payout map[string]PayoutProvider
}

func NewRegistry() *Registry {
	return &Registry{payin: make(map[string]PayinVendor), payout: make(map[string]PayoutProvider)}
}

// AddPayin registers a routing marker under its own Vendor() name. Call this
// only for vendors actually enabled by config.
func (r *Registry) AddPayin(v PayinVendor) {
	r.payin[v.Vendor()] = v
}

// Payin looks up a registered payin verifier by vendor name.
func (r *Registry) Payin(vendor string) (PayinVendor, bool) {
	v, ok := r.payin[vendor]
	return v, ok
}

// AddPayout registers v under its own Vendor() name (docs/roadmap/archive/23 Task T2).
func (r *Registry) AddPayout(v PayoutProvider) {
	r.payout[v.Vendor()] = v
}

// Payout looks up a registered payout provider by vendor name.
func (r *Registry) Payout(vendor string) (PayoutProvider, bool) {
	v, ok := r.payout[vendor]
	return v, ok
}
