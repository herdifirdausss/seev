package ledger

import "go.opentelemetry.io/otel"

// tracer is package-level (like the metrics in metrics.go) so Handle() can
// emit a span without threading an explicit tracer through New().
var tracer = otel.Tracer("github.com/herdifirdausss/seev/internal/ledger")
