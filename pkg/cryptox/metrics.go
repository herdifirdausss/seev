package cryptox

// docs/roadmap/active/51-a8-data-lifecycle-privacy.md K2's "key metrics" / K13's low-cardinality
// convention: key_version is a small, bounded, operator-assigned integer
// (never a table primary key, row ID, or any value derived from
// plaintext) — safe as a label the same way owner/class/action already
// are elsewhere in this repo's metrics.

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var sealTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "seev",
	Subsystem: "cryptox",
	Name:      "seal_total",
	Help:      "Envelope encryption operations, by key version and result.",
}, []string{"key_version", "result"})

var openTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "seev",
	Subsystem: "cryptox",
	Name:      "open_total",
	Help:      "Envelope decryption operations, by key version and result — a nonzero rate of result=\"error\" for an old key version usually means a ciphertext was moved to the wrong row/column, not that the key itself is bad.",
}, []string{"key_version", "result"})

func recordSeal(keyVersion int, err error) {
	sealTotal.WithLabelValues(strconv.Itoa(keyVersion), resultLabel(err)).Inc()
}

func recordOpen(keyVersion int, err error) {
	openTotal.WithLabelValues(strconv.Itoa(keyVersion), resultLabel(err)).Inc()
}

func resultLabel(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}
