package notify

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/notify/model"
)

// newDigestRequest maps one notification to the next local daily send window.
// A window is the interval between two configured local digest times, rather
// than a calendar midnight interval. This keeps a notification created after
// today's send in tomorrow's window and prevents it from being appended to a
// window that has already been delivered.
func newDigestRequest(now time.Time, notificationID, userID uuid.UUID, locale string, settings model.UserSettings) (model.DigestRequest, error) {
	location, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return model.DigestRequest{}, fmt.Errorf("notification digest timezone: %w", err)
	}
	digestHour, err := time.ParseInLocation("15:04", settings.DailyDigestHour, location)
	if err != nil {
		return model.DigestRequest{}, fmt.Errorf("notification digest hour: %w", err)
	}

	localNow := now.In(location)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	todayDigest := time.Date(today.Year(), today.Month(), today.Day(), digestHour.Hour(), digestHour.Minute(), 0, 0, location)
	localDate := today
	scheduledAt := todayDigest
	previous := today.AddDate(0, 0, -1)
	windowStart := time.Date(previous.Year(), previous.Month(), previous.Day(), digestHour.Hour(), digestHour.Minute(), 0, 0, location)
	if !todayDigest.After(localNow) {
		localDate = today.AddDate(0, 0, 1)
		scheduledAt = todayDigest.AddDate(0, 0, 1)
		windowStart = todayDigest
	}
	windowEnd := scheduledAt

	if locale == "" {
		locale = "en-US"
	}
	return model.DigestRequest{
		NotificationID: notificationID,
		UserID:         userID,
		Locale:         locale,
		Timezone:       settings.Timezone,
		LocalDate:      localDate,
		WindowStart:    windowStart.UTC(),
		WindowEnd:      windowEnd.UTC(),
		ScheduledAt:    scheduledAt.UTC(),
	}, nil
}
