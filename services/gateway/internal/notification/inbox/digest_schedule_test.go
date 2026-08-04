package notify

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/services/gateway/internal/notification/model"
)

func TestNewDigestRequestBeforeDigestUsesPreviousWindow(t *testing.T) {
	location, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 1, 7, 30, 0, 0, location)
	request, err := newDigestRequest(now, uuid.New(), uuid.New(), "en-US", model.UserSettings{
		Timezone:        location.String(),
		DailyDigestHour: "08:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := request.LocalDate.Format("2006-01-02"); got != "2026-08-01" {
		t.Fatalf("local date = %s, want 2026-08-01", got)
	}
	if got := request.WindowStart.In(location).Format("2006-01-02 15:04"); got != "2026-07-31 08:00" {
		t.Fatalf("window start = %s, want 2026-07-31 08:00", got)
	}
	if got := request.WindowEnd.In(location).Format("2006-01-02 15:04"); got != "2026-08-01 08:00" {
		t.Fatalf("window end = %s, want 2026-08-01 08:00", got)
	}
}

func TestNewDigestRequestAfterDigestUsesNextWindow(t *testing.T) {
	location, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, location)
	request, err := newDigestRequest(now, uuid.New(), uuid.New(), "id-ID", model.UserSettings{
		Timezone:        location.String(),
		DailyDigestHour: "08:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Locale != "id-ID" {
		t.Fatalf("locale = %s, want id-ID", request.Locale)
	}
	if got := request.LocalDate.Format("2006-01-02"); got != "2026-08-02" {
		t.Fatalf("local date = %s, want 2026-08-02", got)
	}
	if got := request.WindowStart.In(location).Format("2006-01-02 15:04"); got != "2026-08-01 08:00" {
		t.Fatalf("window start = %s, want 2026-08-01 08:00", got)
	}
	if got := request.WindowEnd.In(location).Format("2006-01-02 15:04"); got != "2026-08-02 08:00" {
		t.Fatalf("window end = %s, want 2026-08-02 08:00", got)
	}
}
