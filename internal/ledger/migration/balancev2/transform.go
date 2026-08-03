package balancev2

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Transform is the reviewed, compiled v1 -> v2 shape change. A source row is
// one account projection; account type selects the semantic v2 amount while
// all other amounts remain exactly zero.
func Transform(source SourceRow, transactionID *uuid.UUID) (TargetRow, error) {
	currency := strings.ToUpper(strings.TrimSpace(source.Currency))
	if source.AccountID == uuid.Nil {
		return TargetRow{}, fmt.Errorf("balancev2: account id is required")
	}
	if len(currency) != 3 {
		return TargetRow{}, fmt.Errorf("balancev2: currency must be three characters for account %s", source.AccountID)
	}
	accountType := strings.ToLower(strings.TrimSpace(source.AccountType))
	if accountType == "" {
		return TargetRow{}, fmt.Errorf("balancev2: account type is required for account %s", source.AccountID)
	}
	if source.SourceVersion < 0 {
		return TargetRow{}, fmt.Errorf("balancev2: negative source version for account %s", source.AccountID)
	}

	target := TargetRow{
		AccountID:         source.AccountID,
		AccountType:       accountType,
		Currency:          currency,
		AllowNegative:     source.AllowNegative,
		SourceVersion:     source.SourceVersion,
		LastTransactionID: transactionID,
	}
	switch accountType {
	case "hold":
		target.ReservedAmount = source.Balance
	case "pending":
		target.PendingAmount = source.Balance
	case "frozen":
		target.RestrictedAmount = source.Balance
	case "cash", "pocket", "fee", "settlement", "escrow", "chargeback", "confiscated", "adjustment", "suspense", "fx_conversion", "interest_expense":
		target.AvailableAmount = source.Balance
	default:
		return TargetRow{}, fmt.Errorf("%w: account type %s", ErrUnsupportedLegacyRow, accountType)
	}
	target.ProjectionChecksum = Checksum(target)
	return target, nil
}

// Checksum is diagnostic, not an authorization primitive. Its input is a
// stable binary encoding of the exact financial fields named by the plan.
func Checksum(target TargetRow) []byte {
	var b bytes.Buffer
	b.Write(target.AccountID[:])
	currency := strings.ToUpper(strings.TrimSpace(target.Currency))
	if len(currency) > 3 {
		currency = currency[:3]
	}
	var currencyBytes [3]byte
	copy(currencyBytes[:], currency)
	b.Write(currencyBytes[:])
	b.WriteString(target.AccountType)
	_ = binary.Write(&b, binary.BigEndian, target.AllowNegative)
	for _, amount := range []int64{
		target.AvailableAmount,
		target.ReservedAmount,
		target.PendingAmount,
		target.RestrictedAmount,
	} {
		_ = binary.Write(&b, binary.BigEndian, amount)
	}
	_ = binary.Write(&b, binary.BigEndian, target.SourceVersion)
	sum := sha256.Sum256(b.Bytes())
	return sum[:]
}

func ChecksumMatches(target TargetRow) bool {
	return bytes.Equal(target.ProjectionChecksum, Checksum(target))
}

	// CompareRows classifies a source/target observation without tolerances. A
	// target ahead of the authoritative source is a version-regression signal,
	// not a harmless money mismatch: source ordering is monotonic, so the row
	// must be blocked until an operator verifies the source/target history.
func CompareRows(source SourceRow, target *TargetRow) Comparison {
	comparison := Comparison{AccountID: source.AccountID, ResourceLayer: "source_target", SourceVersion: source.SourceVersion}
	comparison.SourceChecksum = ChecksumForSource(source)
	if target == nil {
		comparison.Result = "target_missing"
		comparison.Classification = ClassificationBackfillMissing
		comparison.Severity = "critical"
		comparison.FieldMask = FieldTargetMissing
		return comparison
	}
	comparison.TargetVersion = target.SourceVersion
	comparison.TargetVersionSet = true
	comparison.TargetChecksum = append([]byte(nil), target.ProjectionChecksum...)
	if target.SourceVersion > source.SourceVersion {
		comparison.Result = "target_ahead"
		comparison.Classification = ClassificationVersionRegression
		comparison.Severity = "critical"
		comparison.FieldMask = FieldVersion
		return comparison
	}
	if target.SourceVersion < source.SourceVersion {
		comparison.Result = "target_stale"
		comparison.Classification = ClassificationStaleBackfill
		comparison.Severity = "critical"
		comparison.FieldMask |= FieldVersion
	}
	if !ChecksumMatches(*target) {
		comparison.Result = "value_mismatch"
		comparison.Classification = ClassificationTargetCorruption
		comparison.Severity = "critical"
		comparison.FieldMask |= FieldChecksum
	}
	expected, err := Transform(source, target.LastTransactionID)
	if err != nil {
		comparison.Result = "unsupported"
		comparison.Classification = ClassificationTransformBug
		if errors.Is(err, ErrUnsupportedLegacyRow) {
			comparison.Classification = ClassificationUnsupportedLegacyRow
		}
		comparison.Severity = "critical"
		return comparison
	}
	if expected.Currency != strings.ToUpper(strings.TrimSpace(target.Currency)) {
		comparison.Result = "currency_mismatch"
		comparison.Classification = ClassificationTargetCorruption
		comparison.Severity = "critical"
		comparison.FieldMask |= FieldCurrency
	}
	if expected.AccountType != target.AccountType {
		comparison.Result = "account_type_mismatch"
		comparison.Classification = ClassificationTargetCorruption
		comparison.Severity = "critical"
		comparison.FieldMask |= FieldAccountType
	}
	if expected.AllowNegative != target.AllowNegative {
		comparison.Result = "allow_negative_mismatch"
		comparison.Classification = ClassificationTargetCorruption
		comparison.Severity = "critical"
		comparison.FieldMask |= FieldAllowNegative
	}
	if expected.AvailableAmount != target.AvailableAmount {
		comparison.Result = "value_mismatch"
		comparison.Classification = ClassificationTargetCorruption
		comparison.Severity = "critical"
		comparison.FieldMask |= FieldAvailable
	}
	if expected.ReservedAmount != target.ReservedAmount {
		comparison.Result = "value_mismatch"
		comparison.Classification = ClassificationTargetCorruption
		comparison.Severity = "critical"
		comparison.FieldMask |= FieldReserved
	}
	if expected.PendingAmount != target.PendingAmount {
		comparison.Result = "value_mismatch"
		comparison.Classification = ClassificationTargetCorruption
		comparison.Severity = "critical"
		comparison.FieldMask |= FieldPending
	}
	if expected.RestrictedAmount != target.RestrictedAmount {
		comparison.Result = "value_mismatch"
		comparison.Classification = ClassificationTargetCorruption
		comparison.Severity = "critical"
		comparison.FieldMask |= FieldRestricted
	}
	if comparison.FieldMask == 0 {
		comparison.Result = "match"
		comparison.Classification = "match"
		comparison.Severity = "none"
	}
	return comparison
}

func ChecksumForSource(source SourceRow) []byte {
	target, err := Transform(source, nil)
	if err != nil {
		return nil
	}
	return target.ProjectionChecksum
}
