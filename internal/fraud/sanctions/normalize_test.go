package sanctions

import "testing"

func TestNormalizeNameFoldsDiacriticsAndSortsTokens(t *testing.T) {
	if got := NormalizeName("Élodie  van-Der Meer"); got != "der elodie meer van" {
		t.Fatalf("NormalizeName() = %q", got)
	}
}
