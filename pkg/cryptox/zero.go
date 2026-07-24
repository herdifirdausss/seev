package cryptox

// Zero overwrites b in place — best-effort defense in depth against a
// decrypted DEK or plaintext lingering in memory longer than needed. Go's
// GC and the runtime's own copying/moving of stack-allocated slices mean
// this is not a hard guarantee (the compiler can and does elide dead
// stores in some cases), but it costs nothing and it's what every caller
// in this package already does with defer immediately after use.
func Zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
