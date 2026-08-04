package fx

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
)

var (
	fxQuotesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "fx", Name: "quotes_total",
		Help: "FX quote attempts by currency pair, direction, and bounded result.",
	}, []string{"pair", "direction", "result"})
	fxQuoteDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "seev", Subsystem: "fx", Name: "quote_duration_seconds",
		Help: "FX quote latency by currency pair, direction, and bounded result.",
	}, []string{"pair", "direction", "result"})
	fxConversionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "fx", Name: "conversions_total",
		Help: "FX conversion attempts by currency pair, direction, and bounded result.",
	}, []string{"pair", "direction", "result"})
	fxConversionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "seev", Subsystem: "fx", Name: "conversion_duration_seconds",
		Help: "FX conversion latency by currency pair, direction, and bounded result.",
	}, []string{"pair", "direction", "result"})
	fxQuoteExpiredTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "fx", Name: "quote_expired_total",
		Help: "FX quotes rejected because they were expired or already outside their consumable window.",
	}, []string{"pair", "direction"})
	fxPositionLimitDecisionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "fx", Name: "position_limit_decisions_total",
		Help: "FX position hard-limit decisions by pair, currency, and result.",
	}, []string{"pair", "currency", "result"})
	fxPositionBalance = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "seev", Subsystem: "fx", Name: "position_balance",
		Help: "Current synthetic FX position balance in the position account's minor units.",
	}, []string{"pair", "currency"})
	fxPositionUtilization = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "seev", Subsystem: "fx", Name: "position_utilization_ratio",
		Help: "Absolute synthetic FX position utilization against a bounded limit band.",
	}, []string{"pair", "currency", "bound"})
	fxCurrencyMismatchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "fx", Name: "currency_mismatch_total",
		Help: "FX currency-boundary mismatches rejected before a conversion can post.",
	}, []string{"boundary", "reason"})
	fxAssuranceFindingsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "fx", Name: "assurance_findings_total",
		Help: "FX reconciliation findings by bounded finding and severity.",
	}, []string{"finding", "severity"})
	currencyOperationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Name: "currency_operations_total",
		Help: "Currency operation decisions by bounded operation, currency, and result.",
	}, []string{"operation", "currency", "result"})
	currencyPolicyDecisionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Name: "currency_policy_decisions_total",
		Help: "Currency policy decisions by bounded operation, currency, result, and reason.",
	}, []string{"operation", "currency", "result", "reason"})
	currencyAccountProvisionTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Name: "currency_account_provision_total",
		Help: "Currency account-family provisioning attempts by currency and result.",
	}, []string{"currency", "result"})
)

func observeCurrencyOperation(operation, code string, err error) {
	operation = boundedOperation(operation)
	currency := boundedMetricCode(code)
	result := fxMetricResult(err)
	currencyOperationsTotal.WithLabelValues(operation, currency, result).Inc()
	currencyPolicyDecisionsTotal.WithLabelValues(operation, currency, result, currencyMetricReason(err)).Inc()
}

func ObserveCurrencyAccountProvision(code string, err error) {
	currencyAccountProvisionTotal.WithLabelValues(boundedMetricCode(code), fxMetricResult(err)).Inc()
}

func observeFXQuote(source, target string, started time.Time, err error) {
	pair, direction := fxLabels(source, target)
	result := fxMetricResult(err)
	fxQuotesTotal.WithLabelValues(pair, direction, result).Inc()
	fxQuoteDuration.WithLabelValues(pair, direction, result).Observe(time.Since(started).Seconds())
	if errors.Is(err, apperror.ErrFXQuoteExpired) {
		fxQuoteExpiredTotal.WithLabelValues(pair, direction).Inc()
	}
	if errors.Is(err, apperror.ErrCurrencyMismatch) || errors.Is(err, apperror.ErrCurrencyInvalid) {
		fxCurrencyMismatchTotal.WithLabelValues("quote", fxMetricReason(err)).Inc()
	}
}

func observeFXConversion(source, target string, started time.Time, err error) {
	pair, direction := fxLabels(source, target)
	result := fxMetricResult(err)
	fxConversionsTotal.WithLabelValues(pair, direction, result).Inc()
	fxConversionDuration.WithLabelValues(pair, direction, result).Observe(time.Since(started).Seconds())
	if errors.Is(err, apperror.ErrCurrencyMismatch) || errors.Is(err, apperror.ErrCurrencyInvalid) {
		fxCurrencyMismatchTotal.WithLabelValues("conversion", fxMetricReason(err)).Inc()
	}
}

func observePositionMetrics(pair, code string, balance, minimum, maximum, warningMinimum, warningMaximum, criticalMinimum, criticalMaximum int64) {
	pair = boundedPairCode(pair)
	code = boundedMetricCode(code)
	fxPositionBalance.WithLabelValues(pair, code).Set(float64(balance))
	fxPositionUtilization.WithLabelValues(pair, code, "warning").Set(utilizationRatio(balance, warningMinimum, warningMaximum))
	fxPositionUtilization.WithLabelValues(pair, code, "critical").Set(utilizationRatio(balance, criticalMinimum, criticalMaximum))
	fxPositionUtilization.WithLabelValues(pair, code, "maximum").Set(utilizationRatio(balance, minimum, maximum))
}

func observePositionLimitDecision(pair, code, result string) {
	fxPositionLimitDecisionsTotal.WithLabelValues(boundedPairCode(pair), boundedMetricCode(code), result).Inc()
}

func fxLabels(source, target string) (string, string) {
	source = boundedMetricCode(source)
	target = boundedMetricCode(target)
	if source > target {
		return target + source, source + "_TO_" + target
	}
	return source + target, source + "_TO_" + target
}

func boundedMetricCode(raw string) string {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if len(code) == 3 && code[0] >= 'A' && code[0] <= 'Z' && code[1] >= 'A' && code[1] <= 'Z' && code[2] >= 'A' && code[2] <= 'Z' {
		return code
	}
	if code == "" {
		return "unknown"
	}
	return "invalid"
}

func boundedPairCode(raw string) string {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if len(code) >= 6 && len(code) <= 32 {
		for i := 0; i < len(code); i++ {
			if code[i] < 'A' || code[i] > 'Z' {
				return "invalid"
			}
		}
		return code
	}
	if code == "" {
		return "unknown"
	}
	return "invalid"
}

func fxMetricResult(err error) string {
	if err == nil {
		return "success"
	}
	for _, sentinel := range []error{
		apperror.ErrValidation, apperror.ErrCurrencyInvalid, apperror.ErrCurrencyDisabled,
		apperror.ErrCurrencyOperationDisabled, apperror.ErrCurrencyAccountMissing,
		apperror.ErrInsufficientFunds, apperror.ErrCurrencyLimitExceeded,
		apperror.ErrFXQuoteExpired, apperror.ErrFXQuoteMismatch,
		apperror.ErrFXQuoteAlreadyConsumed, apperror.ErrFXDirectionDisabled,
		apperror.ErrFXConversionsPaused, apperror.ErrFXPositionLimitExceeded,
		apperror.ErrIdempotencyConflict,
	} {
		if errors.Is(err, sentinel) {
			return "rejected"
		}
	}
	return "error"
}

func fxMetricReason(err error) string {
	switch {
	case errors.Is(err, apperror.ErrCurrencyInvalid):
		return "invalid_currency"
	case errors.Is(err, apperror.ErrCurrencyMismatch):
		return "account_currency_mismatch"
	default:
		return "boundary_mismatch"
	}
}

func currencyMetricReason(err error) string {
	switch {
	case errors.Is(err, apperror.ErrCurrencyInvalid):
		return "invalid_currency"
	case errors.Is(err, apperror.ErrCurrencyDisabled):
		return "disabled"
	case errors.Is(err, apperror.ErrCurrencyOperationDisabled):
		return "operation_disabled"
	case errors.Is(err, apperror.ErrCurrencyNotEnabled):
		return "not_enabled"
	default:
		return "none"
	}
}

func boundedOperation(raw string) string {
	operation := strings.ToLower(strings.TrimSpace(raw))
	if operation == "" {
		return "unknown"
	}
	for _, r := range operation {
		if (r < 'a' || r > 'z') && r != '_' {
			return "invalid"
		}
	}
	if len(operation) > 32 {
		return "invalid"
	}
	return operation
}

func utilizationRatio(balance, minimum, maximum int64) float64 {
	bound := math.Max(math.Abs(float64(minimum)), math.Abs(float64(maximum)))
	if bound == 0 || math.IsInf(bound, 0) || math.IsNaN(bound) {
		return 0
	}
	return math.Abs(float64(balance)) / bound
}
