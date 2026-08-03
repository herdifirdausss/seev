package fx

import "math/big"

// positionBands derives operational bands from hard limits without using
// floating point. The critical band sits closer to the hard boundary than the
// warning band, so a position can move from normal to warning to critical
// before it is rejected by the transactional hard-limit check.
func positionBands(minimum, maximum int64) (warningMinimum, warningMaximum, criticalMinimum, criticalMaximum int64) {
	minimumBig := big.NewInt(minimum)
	maximumBig := big.NewInt(maximum)
	span := new(big.Int).Sub(new(big.Int).Set(maximumBig), minimumBig)

	warningMinimum = interpolateFromMinimum(minimumBig, span, 10)
	criticalMinimum = interpolateFromMinimum(minimumBig, span, 20)
	warningMaximum = interpolateFromMaximum(maximumBig, span, 10)
	criticalMaximum = interpolateFromMaximum(maximumBig, span, 20)
	return warningMinimum, warningMaximum, criticalMinimum, criticalMaximum
}

func interpolateFromMinimum(minimum, span *big.Int, denominator int64) int64 {
	step := new(big.Int).Quo(new(big.Int).Set(span), big.NewInt(denominator))
	return new(big.Int).Add(new(big.Int).Set(minimum), step).Int64()
}

func interpolateFromMaximum(maximum, span *big.Int, denominator int64) int64 {
	step := new(big.Int).Quo(new(big.Int).Set(span), big.NewInt(denominator))
	return new(big.Int).Sub(new(big.Int).Set(maximum), step).Int64()
}

func positionState(balance, minimum, maximum, warningMinimum, warningMaximum, criticalMinimum, criticalMaximum int64) string {
	switch {
	case balance < minimum || balance > maximum:
		return "limit_exceeded"
	case balance < criticalMinimum || balance > criticalMaximum:
		return "critical"
	case balance < warningMinimum || balance > warningMaximum:
		return "warning"
	default:
		return "normal"
	}
}
