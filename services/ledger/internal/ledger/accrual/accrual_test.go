package accrual

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestDailyInterest_TableDriven(t *testing.T) {
	cases := []struct {
		name    string
		balance decimal.Decimal
		rateBps int
		want    decimal.Decimal
	}{
		{
			name:    "floor rounding",
			balance: decimal.NewFromInt(1_000_000), // 1,000,000 * 500 / 10000 / 365 = 136.98... -> 136
			rateBps: 500,                           // 5% annual
			want:    decimal.NewFromInt(136),
		},
		{
			name:    "zero rate",
			balance: decimal.NewFromInt(1_000_000),
			rateBps: 0,
			want:    decimal.Zero,
		},
		{
			name:    "zero balance",
			balance: decimal.Zero,
			rateBps: 500,
			want:    decimal.Zero,
		},
		{
			name:    "negative balance never accrues",
			balance: decimal.NewFromInt(-1_000_000),
			rateBps: 500,
			want:    decimal.Zero,
		},
		{
			name:    "small balance rounds down to zero",
			balance: decimal.NewFromInt(10), // 10 * 500 / 10000 / 365 = 0.00136... -> 0
			rateBps: 500,
			want:    decimal.Zero,
		},
		{
			name:    "max rate 20%",
			balance: decimal.NewFromInt(10_000_000), // 10,000,000 * 2000 / 10000 / 365 = 5479.45... -> 5479
			rateBps: 2000,
			want:    decimal.NewFromInt(5479),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DailyInterest(c.balance, c.rateBps)
			assert.True(t, c.want.Equal(got), "case %q: want %s, got %s", c.name, c.want, got)
		})
	}
}
