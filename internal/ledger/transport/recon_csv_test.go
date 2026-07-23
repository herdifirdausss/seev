package transport

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── parseReconCSV (docs/roadmap/archive/16 Task T2 "Required test": malformed CSV /
// decimal amount / >50k rows → 400 with a clear message) ────────────────────

func TestParseReconCSV_Valid_Succeeds(t *testing.T) {
	csv := "external_ref,amount,settled_at\nref-1,10000,2026-07-12\nref-2,20000,2026-07-12\n"
	rows, err := parseReconCSV(strings.NewReader(csv))

	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "ref-1", rows[0].ExternalRef)
	assert.True(t, rows[0].Amount.Equal(decimal.NewFromInt(10000)))
	assert.Equal(t, "2026-07-12", rows[0].SettledAt)
}

func TestParseReconCSV_ColumnOrderFlexible(t *testing.T) {
	csv := "settled_at,external_ref,amount\n2026-07-12,ref-1,10000\n"
	rows, err := parseReconCSV(strings.NewReader(csv))

	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "ref-1", rows[0].ExternalRef)
}

func TestParseReconCSV_MissingRequiredColumn_Rejected(t *testing.T) {
	csv := "external_ref,amount\nref-1,10000\n"
	_, err := parseReconCSV(strings.NewReader(csv))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "settled_at")
}

func TestParseReconCSV_MalformedRow_Rejected(t *testing.T) {
	// Unterminated quote makes the second row unparsable as CSV.
	csv := "external_ref,amount,settled_at\n\"ref-1,10000,2026-07-12\n"
	_, err := parseReconCSV(strings.NewReader(csv))

	require.Error(t, err)
}

func TestParseReconCSV_NonIntegralAmount_Rejected(t *testing.T) {
	csv := "external_ref,amount,settled_at\nref-1,100.50,2026-07-12\n"
	_, err := parseReconCSV(strings.NewReader(csv))

	require.Error(t, err)
	assert.ErrorIs(t, err, errNonIntegralAmount)
}

func TestParseReconCSV_NonNumericAmount_Rejected(t *testing.T) {
	csv := "external_ref,amount,settled_at\nref-1,not-a-number,2026-07-12\n"
	_, err := parseReconCSV(strings.NewReader(csv))

	require.Error(t, err)
}

func TestParseReconCSV_TooManyRows_Rejected(t *testing.T) {
	var b strings.Builder
	b.WriteString("external_ref,amount,settled_at\n")
	for i := 0; i < maxReconCSVRows+1; i++ {
		b.WriteString("ref,100,2026-07-12\n")
	}

	_, err := parseReconCSV(strings.NewReader(b.String()))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "split the file")
}

func TestParseReconCSV_EmptyFile_Rejected(t *testing.T) {
	_, err := parseReconCSV(strings.NewReader(""))
	require.Error(t, err)
}
