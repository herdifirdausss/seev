package notify

import (
	"testing"
	"time"
)

func TestNextAllowedTimeCrossMidnight(t *testing.T) {
	location, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	start, end := "22:00", "06:00"
	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "before midnight",
			now:  time.Date(2026, 8, 1, 23, 0, 0, 0, location),
			want: time.Date(2026, 8, 2, 6, 0, 0, 0, location),
		},
		{
			name: "after midnight",
			now:  time.Date(2026, 8, 2, 2, 0, 0, 0, location),
			want: time.Date(2026, 8, 2, 6, 0, 0, 0, location),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NextAllowedTime(tc.now, location.String(), true, &start, &end)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(tc.want.UTC()) {
				t.Fatalf("next allowed time = %s, want %s", got, tc.want.UTC())
			}
		})
	}
}

func TestNextAllowedTimeOutsideQuietHoursIsUnchanged(t *testing.T) {
	location, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	start, end := "22:00", "06:00"
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, location)
	got, err := NextAllowedTime(now, location.String(), true, &start, &end)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(now) {
		t.Fatalf("next allowed time = %s, want unchanged %s", got, now)
	}
}
