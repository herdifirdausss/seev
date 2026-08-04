package notify

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/gateway/internal/notification/model"
	"github.com/herdifirdausss/seev/services/gateway/internal/notification/registry"
	"github.com/herdifirdausss/seev/services/gateway/internal/notification/repository"
)

func (m *Module) SettingsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		if m.platform == nil || m.db == nil {
			response.ServiceUnavailable(w, "NOTIFICATION_CHANNEL_UNAVAILABLE", "notification settings are unavailable")
			return
		}
		if r.Method == http.MethodGet {
			settings, err := m.platform.GetSettings(r.Context(), userID)
			if err != nil {
				response.InternalServerError(w, err)
				return
			}
			settings = m.applySettingsDefaults(settings)
			response.OK(w, settings)
			return
		}
		var input model.UserSettings
		if !response.Decode(w, r, &input) {
			return
		}
		input.UserID = userID
		if err := validateSettings(input); err != nil {
			response.ErrorStatus(w, http.StatusBadRequest, "NOTIFICATION_SETTINGS_INVALID", err.Error())
			return
		}
		settings, err := m.platform.PutSettings(r.Context(), input, input.Version)
		if errors.Is(err, repository.ErrSettingsConflict) {
			response.ErrorStatus(w, http.StatusConflict, "NOTIFICATION_SETTINGS_CONFLICT", "notification settings version is stale")
			return
		}
		if err != nil {
			response.InternalServerError(w, err)
			return
		}
		response.OK(w, settings)
	}
}

func validateSettings(settings model.UserSettings) error {
	if settings.Locale != "en-US" && settings.Locale != "id-ID" {
		return errors.New("locale must be en-US or id-ID")
	}
	if _, err := time.LoadLocation(settings.Timezone); err != nil {
		return errors.New("timezone must be a valid IANA timezone")
	}
	if settings.DailyDigestHour == "" {
		return errors.New("daily_digest_hour is required")
	}
	if _, err := time.Parse("15:04", settings.DailyDigestHour); err != nil {
		return errors.New("daily_digest_hour must be HH:MM")
	}
	if settings.QuietHoursEnabled {
		if settings.QuietHoursStart == nil || settings.QuietHoursEnd == nil {
			return errors.New("quiet hours start and end are required")
		}
		if _, err := time.Parse("15:04", *settings.QuietHoursStart); err != nil {
			return errors.New("quiet_hours_start must be HH:MM")
		}
		if _, err := time.Parse("15:04", *settings.QuietHoursEnd); err != nil {
			return errors.New("quiet_hours_end must be HH:MM")
		}
	}
	if settings.Version < 1 {
		return errors.New("version must be positive")
	}
	return nil
}

type preferenceRequest struct {
	Preferences []model.Preference `json:"preferences"`
}
type preferenceResponse struct {
	Preferences []model.Preference           `json:"preferences"`
	Effective   map[string]map[string]string `json:"effective"`
}

func (m *Module) PreferencesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		if m.platform == nil || m.db == nil {
			response.ServiceUnavailable(w, "NOTIFICATION_CHANNEL_UNAVAILABLE", "notification preferences are unavailable")
			return
		}
		if r.Method == http.MethodGet {
			prefs, err := m.platform.ListPreferences(r.Context(), userID)
			if err != nil {
				response.InternalServerError(w, err)
				return
			}
			response.OK(w, m.effectivePreferences(prefs))
			return
		}
		var req preferenceRequest
		if !response.Decode(w, r, &req) {
			return
		}
		for i := range req.Preferences {
			p := &req.Preferences[i]
			p.UserID = userID
			if !validCategory(p.Category) || !validChannel(p.Channel) || !validMode(p.Mode) ||
				(p.Mode == model.ModeDailyDigest && p.Channel != model.ChannelEmail) {
				response.ErrorStatus(w, http.StatusBadRequest, "NOTIFICATION_PREFERENCE_INVALID", "invalid notification preference")
				return
			}
			if p.Channel == model.ChannelInApp && p.Mode != model.ModeImmediate {
				response.ErrorStatus(w, http.StatusBadRequest, "NOTIFICATION_PREFERENCE_MANDATORY", "mandatory in-app notifications cannot be disabled")
				return
			}
		}
		prefs, err := m.platform.ReplacePreferences(r.Context(), userID, req.Preferences)
		if errors.Is(err, repository.ErrPreferenceConflict) {
			response.ErrorStatus(w, http.StatusConflict, "NOTIFICATION_PREFERENCE_CONFLICT", "notification preference version is stale")
			return
		}
		if err != nil {
			response.InternalServerError(w, err)
			return
		}
		for _, preference := range req.Preferences {
			notificationPreferenceUpdatesTotal.WithLabelValues(preference.Channel, preference.Mode, "success").Inc()
		}
		response.OK(w, m.effectivePreferences(prefs))
	}
}

func (m *Module) effectivePreferences(prefs []model.Preference) preferenceResponse {
	effective := map[string]map[string]string{}
	for _, kind := range registry.All() {
		if _, ok := effective[kind.Category]; !ok {
			effective[kind.Category] = map[string]string{}
		}
		for channelName, mode := range kind.DefaultModes {
			if (channelName == model.ChannelEmail && !m.config.EmailEnabled) || (channelName == model.ChannelPush && !m.config.PushEnabled) {
				mode = model.ModeDisabled
			}
			if channelName == model.ChannelEmail && mode == model.ModeDailyDigest && !m.config.DigestEnabled {
				mode = model.ModeDisabled
			}
			effective[kind.Category][channelName] = mode
		}
	}
	for _, p := range prefs {
		if _, ok := effective[p.Category]; !ok {
			effective[p.Category] = map[string]string{}
		}
		if p.Channel == model.ChannelInApp {
			effective[p.Category][p.Channel] = model.ModeImmediate
		} else {
			effective[p.Category][p.Channel] = p.Mode
		}
	}
	return preferenceResponse{Preferences: prefs, Effective: effective}
}

func (m *Module) applySettingsDefaults(settings model.UserSettings) model.UserSettings {
	if !settings.CreatedAt.IsZero() {
		return settings
	}
	if settings.Locale == "" {
		settings.Locale = m.config.DefaultLocale
	}
	if settings.Timezone == "" {
		settings.Timezone = m.config.DefaultTimezone
	}
	if settings.DailyDigestHour == "" {
		hour := m.config.DigestHour
		if hour < 0 || hour > 23 {
			hour = 8
		}
		settings.DailyDigestHour = fmt.Sprintf("%02d:00", hour)
	}
	if settings.Version < 1 {
		settings.Version = 1
	}
	return settings
}

func validChannel(value string) bool {
	return value == model.ChannelInApp || value == model.ChannelEmail || value == model.ChannelPush
}
func validMode(value string) bool {
	return value == model.ModeImmediate || value == model.ModeDailyDigest || value == model.ModeDisabled
}

type deviceRequest struct {
	Platform   string `json:"platform"`
	Token      string `json:"token"`
	DeviceName string `json:"device_name"`
}
type deviceResponse struct {
	ID              uuid.UUID  `json:"id"`
	Platform        string     `json:"platform"`
	DeviceName      string     `json:"device_name,omitempty"`
	TokenSuffix     string     `json:"token_suffix,omitempty"`
	Status          string     `json:"status"`
	LastSuccessAt   *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt   *time.Time `json:"last_failure_at,omitempty"`
	LastFailureCode string     `json:"last_failure_code,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
}

func toDeviceResponse(d model.DeviceEndpoint) deviceResponse {
	return deviceResponse{ID: d.ID, Platform: d.Platform, DeviceName: d.DeviceName, TokenSuffix: d.TokenSuffix,
		Status: d.Status, LastSuccessAt: d.LastSuccessAt, LastFailureAt: d.LastFailureAt, LastFailureCode: d.LastFailureCode,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, RevokedAt: d.RevokedAt}
}

func (m *Module) DevicesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		if m.platform == nil || m.db == nil {
			response.ServiceUnavailable(w, "NOTIFICATION_CHANNEL_UNAVAILABLE", "notification devices are unavailable")
			return
		}
		if r.Method == http.MethodGet {
			devices, err := m.platform.ListDevices(r.Context(), userID)
			if err != nil {
				response.InternalServerError(w, err)
				return
			}
			out := make([]deviceResponse, 0, len(devices))
			for _, d := range devices {
				out = append(out, toDeviceResponse(d))
			}
			response.OK(w, map[string]any{"devices": out})
			return
		}
		var req deviceRequest
		if !response.Decode(w, r, &req) {
			return
		}
		if req.Platform != "android" && req.Platform != "ios" && req.Platform != "web" && req.Platform != "test" {
			response.ErrorStatus(w, http.StatusBadRequest, "NOTIFICATION_DEVICE_INVALID", "unsupported device platform")
			return
		}
		if len(req.Token) < 1 || len(req.Token) > 4096 {
			response.ErrorStatus(w, http.StatusBadRequest, "NOTIFICATION_DEVICE_INVALID", "device token length is invalid")
			return
		}
		if len(req.DeviceName) > 128 {
			response.ErrorStatus(w, http.StatusBadRequest, "NOTIFICATION_DEVICE_INVALID", "device name is too long")
			return
		}
		fingerprint := m.config.fingerprint(req.Token)
		if existing, found, err := m.platform.FindDeviceByFingerprint(r.Context(), userID, fingerprint); err != nil {
			response.InternalServerError(w, err)
			return
		} else if found {
			if m.config.EncryptionRing == nil {
				response.ServiceUnavailable(w, "NOTIFICATION_CHANNEL_UNAVAILABLE", "device encryption is unavailable")
				return
			}
			ciphertext, sealErr := m.config.EncryptionRing.Seal(cryptox.AAD{Service: "gateway", Table: "notif_device_endpoints", Column: "token", RowID: existing.ID.String()}, []byte(req.Token))
			if sealErr != nil {
				response.InternalServerError(w, sealErr)
				return
			}
			existing.Platform, existing.DeviceName, existing.Status = req.Platform, strings.TrimSpace(req.DeviceName), "active"
			existing.TokenCiphertext = ciphertext
			existing.TokenKeyVersion = m.config.EncryptionRing.CurrentVersion()
			existing.TokenFingerprint = fingerprint
			existing.TokenSuffix = tokenSuffix(req.Token)
			endpoint, err := m.platform.RegisterDevice(r.Context(), existing)
			if err != nil {
				response.InternalServerError(w, err)
				return
			}
			notificationDevicesTotal.WithLabelValues(endpoint.Platform, endpoint.Status).Inc()
			response.OK(w, toDeviceResponse(endpoint))
			return
		}
		devices, err := m.platform.ListActiveDevices(r.Context(), userID)
		if err != nil {
			response.InternalServerError(w, err)
			return
		}
		if len(devices) >= m.config.MaxDevices {
			response.ErrorStatus(w, http.StatusConflict, "NOTIFICATION_DEVICE_LIMIT", "active device limit reached")
			return
		}
		if m.config.EncryptionRing == nil {
			response.ServiceUnavailable(w, "NOTIFICATION_CHANNEL_UNAVAILABLE", "device encryption is unavailable")
			return
		}
		id := uuid.New()
		ciphertext, err := m.config.EncryptionRing.Seal(cryptox.AAD{Service: "gateway", Table: "notif_device_endpoints", Column: "token", RowID: id.String()}, []byte(req.Token))
		if err != nil {
			response.InternalServerError(w, err)
			return
		}
		endpoint, err := m.platform.RegisterDevice(r.Context(), model.DeviceEndpoint{ID: id, UserID: userID, Platform: req.Platform,
			DeviceName: strings.TrimSpace(req.DeviceName), TokenCiphertext: ciphertext, TokenKeyVersion: m.config.EncryptionRing.CurrentVersion(),
			TokenFingerprint: fingerprint, TokenSuffix: tokenSuffix(req.Token), Status: "active"})
		if errors.Is(err, repository.ErrDeviceConflict) {
			response.ErrorStatus(w, http.StatusConflict, "NOTIFICATION_DEVICE_INVALID", "device token is already registered to another user")
			return
		}
		if err != nil {
			response.InternalServerError(w, err)
			return
		}
		notificationDevicesTotal.WithLabelValues(endpoint.Platform, endpoint.Status).Inc()
		response.Created(w, toDeviceResponse(endpoint))
	}
}

func (m *Module) RevokeDeviceHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			response.ErrorStatus(w, http.StatusBadRequest, "NOTIFICATION_DEVICE_INVALID", "invalid device id")
			return
		}
		if m.platform == nil || m.db == nil {
			response.ServiceUnavailable(w, "NOTIFICATION_CHANNEL_UNAVAILABLE", "notification devices are unavailable")
			return
		}
		if err := m.platform.RevokeDevice(r.Context(), userID, id); err != nil {
			response.InternalServerError(w, err)
			return
		}
		notificationDevicesTotal.WithLabelValues("unknown", "revoked").Inc()
		response.NoContent(w)
	}
}

func tokenSuffix(token string) string {
	if len(token) <= 4 {
		return token
	}
	return token[len(token)-4:]
}
