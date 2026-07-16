package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

// currentUserID extracts and parses the authenticated user's ID from the
// JWT claims already validated by pkg/middleware.WithAuth — same helper
// shape as internal/payout/http.go.
func currentUserID(r *http.Request) (uuid.UUID, bool) {
	raw := middleware.UserIDFromCtx(r.Context())
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

type userResponse struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	Role      string    `json:"role"`
	KYCLevel  int       `json:"kyc_level"`
	CreatedAt time.Time `json:"created_at"`
}

func toUserResponse(u User) userResponse {
	return userResponse{ID: u.ID, Email: u.Email, FullName: u.FullName, Role: u.Role, KYCLevel: u.KYCLevel, CreatedAt: u.CreatedAt}
}

type tokenResponse struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

func toTokenResponse(p TokenPair) tokenResponse {
	return tokenResponse(p)
}

type authResponse struct {
	User   userResponse  `json:"user"`
	Tokens tokenResponse `json:"tokens"`
}

// writeAuthError maps the module's sentinel errors to HTTP statuses —
// transport-layer concern kept out of the facade, same convention as every
// other module.
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrValidation):
		response.BadRequest(w, err.Error())
	case errors.Is(err, ErrEmailTaken):
		response.Conflict(w, "email already registered")
	case errors.Is(err, ErrInvalidCredentials):
		response.Unauthorized(w, "invalid email or password")
	case errors.Is(err, ErrInvalidRefreshToken):
		response.Unauthorized(w, "invalid refresh token")
	case errors.Is(err, ErrUserDisabled):
		response.Forbidden(w, "account disabled")
	case errors.Is(err, ErrKYCLevelSequence):
		response.BadRequest(w, "level_requested must be the next KYC level")
	case errors.Is(err, ErrKYCPending), errors.Is(err, repository.ErrKYCSubmissionNotPending):
		response.Conflict(w, "a KYC submission is already pending or decided")
	case errors.Is(err, repository.ErrKYCSubmissionNotFound):
		response.NotFound(w, "KYC submission not found")
	default:
		response.InternalServerError(w, err)
	}
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"full_name"`
}

// RegisterHandler serves POST /api/v1/auth/register.
func (m *Module) RegisterHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req registerRequest
		if !response.Decode(w, r, &req) {
			return
		}
		u, pair, err := m.Register(r.Context(), req.Email, req.Password, req.FullName)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		response.Created(w, authResponse{User: toUserResponse(u), Tokens: toTokenResponse(pair)})
	}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginHandler serves POST /api/v1/auth/login.
func (m *Module) LoginHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if !response.Decode(w, r, &req) {
			return
		}
		u, pair, err := m.Login(r.Context(), req.Email, req.Password)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		response.OK(w, authResponse{User: toUserResponse(u), Tokens: toTokenResponse(pair)})
	}
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// RefreshHandler serves POST /api/v1/auth/refresh.
func (m *Module) RefreshHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req refreshRequest
		if !response.Decode(w, r, &req) {
			return
		}
		if req.RefreshToken == "" {
			response.BadRequest(w, "refresh_token is required")
			return
		}
		u, pair, err := m.Refresh(r.Context(), req.RefreshToken)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		response.OK(w, authResponse{User: toUserResponse(u), Tokens: toTokenResponse(pair)})
	}
}

// MeHandler serves GET /api/v1/users/me (authed chain — WithAuth already ran).
func (m *Module) MeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		u, err := m.Me(r.Context(), userID)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		response.OK(w, toUserResponse(u))
	}
}

type updateMeRequest struct {
	FullName string `json:"full_name"`
}

// UpdateMeHandler serves PUT /api/v1/users/me (authed chain).
func (m *Module) UpdateMeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		var req updateMeRequest
		if !response.Decode(w, r, &req) {
			return
		}
		u, err := m.UpdateMe(r.Context(), userID, req.FullName)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		response.OK(w, toUserResponse(u))
	}
}

type kycSubmissionRequest struct {
	LevelRequested int            `json:"level_requested"`
	Payload        map[string]any `json:"payload"`
}

type kycSubmissionResponse struct {
	ID             uuid.UUID      `json:"id"`
	UserID         uuid.UUID      `json:"user_id"`
	LevelRequested int            `json:"level_requested"`
	Status         string         `json:"status"`
	Payload        map[string]any `json:"payload"`
	Provider       string         `json:"provider"`
	ProviderRef    string         `json:"provider_ref,omitempty"`
	DecidedBy      string         `json:"decided_by,omitempty"`
	DecisionReason string         `json:"decision_reason,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	DecidedAt      *time.Time     `json:"decided_at,omitempty"`
}

func toKYCSubmissionResponse(s model.KYCSubmission) kycSubmissionResponse {
	return kycSubmissionResponse{ID: s.ID, UserID: s.UserID, LevelRequested: s.LevelRequested, Status: s.Status, Payload: s.Payload, Provider: s.Provider, ProviderRef: s.ProviderRef, DecidedBy: s.DecidedBy, DecisionReason: s.DecisionReason, CreatedAt: s.CreatedAt, DecidedAt: s.DecidedAt}
}

type kycStatusResponse struct {
	Level      int                    `json:"kyc_level"`
	Submission *kycSubmissionResponse `json:"latest_submission,omitempty"`
}

func (m *Module) SubmitKYCHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		var req kycSubmissionRequest
		if !response.Decode(w, r, &req) {
			return
		}
		s, err := m.SubmitKYC(r.Context(), userID, req.LevelRequested, req.Payload)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		if s.Status == "approved" || s.Status == "rejected" {
			response.OK(w, toKYCSubmissionResponse(s))
			return
		}
		response.Created(w, toKYCSubmissionResponse(s))
	}
}

func (m *Module) KYCStatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r)
		if !ok {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		status, err := m.KYC(r.Context(), userID)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		out := kycStatusResponse{Level: status.Level}
		if status.Submission != nil {
			converted := toKYCSubmissionResponse(*status.Submission)
			out.Submission = &converted
		}
		response.OK(w, out)
	}
}

func adminID(r *http.Request) string {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		return ""
	}
	return claims.UserID
}

func (m *Module) AdminListKYCHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		submissions, err := m.ListKYCSubmissions(r.Context(), r.URL.Query().Get("status"))
		if err != nil {
			writeAuthError(w, err)
			return
		}
		out := make([]kycSubmissionResponse, len(submissions))
		for i, s := range submissions {
			out[i] = toKYCSubmissionResponse(s)
		}
		response.OK(w, map[string]any{"submissions": out})
	}
}

func submissionID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	return id, err == nil
}

func (m *Module) AdminApproveKYCHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := submissionID(r)
		if !ok {
			response.BadRequest(w, "invalid submission id")
			return
		}
		if err := m.ApproveKYC(r.Context(), id, adminID(r)); err != nil {
			writeAuthError(w, err)
			return
		}
		response.OK(w, map[string]any{"status": "approved", "id": id})
	}
}

func (m *Module) AdminRejectKYCHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := submissionID(r)
		if !ok {
			response.BadRequest(w, "invalid submission id")
			return
		}
		var req struct {
			Reason string `json:"reason"`
		}
		if !response.Decode(w, r, &req) {
			return
		}
		if err := m.RejectKYC(r.Context(), id, adminID(r), req.Reason); err != nil {
			writeAuthError(w, err)
			return
		}
		response.OK(w, map[string]any{"status": "rejected", "id": id})
	}
}
