package cryptox

import "strconv"

// AAD is the associated-data binding K2 requires on every envelope: which
// service, table, column, and row this ciphertext belongs to, plus the
// envelope's own version. GCM authenticates AAD without encrypting it — an
// attacker who copies a ciphertext into a different row, column, or table
// changes the AAD an Open call presents at decrypt time, so authentication
// fails before any plaintext is ever produced. This is the entire
// mechanism behind K2's "a ciphertext copied to another row or field must
// fail authentication" requirement; nothing else in this package enforces
// it.
type AAD struct {
	Service string
	Table   string
	Column  string
	RowID   string
	Version int
}

// bytes is a canonical, unambiguous encoding: NUL is not valid in any of
// service/table/column/row ID in this codebase's own naming conventions,
// so a NUL-delimited join can never collide (e.g. Service="a", Table="b|c"
// vs Service="a|b", Table="c" are the classic ambiguous-join bug this
// avoids).
func (a AAD) bytes() []byte {
	b := make([]byte, 0, len(a.Service)+len(a.Table)+len(a.Column)+len(a.RowID)+16)
	b = append(b, a.Service...)
	b = append(b, 0)
	b = append(b, a.Table...)
	b = append(b, 0)
	b = append(b, a.Column...)
	b = append(b, 0)
	b = append(b, a.RowID...)
	b = append(b, 0)
	b = append(b, strconv.Itoa(a.Version)...)
	return b
}
