package merchant

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/merchant/auth"
	"github.com/herdifirdausss/seev/internal/merchant/lifecycle"
	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/internal/merchant/webhook"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/httpcontract"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

// defaultQuotaBaselineRPM/Burst mirror internal/merchant/quota's own
// defaultPolicy (quota.go) — Plan 57 T8 §16.3's "quota increase above the
// default baseline: checker" is defined relative to exactly these two
// numbers, so they are duplicated here as the single source of truth for
// what "baseline" means at the admin layer (quota.go's defaultPolicy
// stays private to that package; re-declaring the two numbers is cheaper
// than exporting internal enforcement plumbing just for this comparison).
const (
	defaultQuotaBaselineRPM   = 60
	defaultQuotaBaselineBurst = 60
)

// AdminRouter is Plan 57 T8's operator-facing HTTP surface — mounted by
// cmd/gateway behind the internal listener's JWT `authed` chain (the same
// chain ledger/payin/payout already use for their own admin routes) at
// /admin/gateway/ once /api/v1 is stripped by the caller. Admin BFF
// reaches it via its already-existing generic proxy
// (m.proxy("gateway", m.clients.Gateway, "/api/v1/admin/gateway/",
// "/api/v1/admin/gateway/") in internal/adminbff/module.go) — CSRF and
// audit-event emission are already handled there, generically, for every
// request that reaches this router; nothing here needs to duplicate
// either.
func (m *Module) AdminRouter() http.Handler {
	mux := httpcontract.New(httpcontract.Options{Owner: "gateway", Audience: "internal", Contract: "internal-v1"})

	mux.HandleFunc("POST /admin/gateway/tenants", m.adminCreateTenant)
	mux.HandleFunc("GET /admin/gateway/tenants", m.adminListTenants)
	mux.HandleFunc("GET /admin/gateway/tenants/{id}", m.adminGetTenant)
	mux.HandleFunc("POST /admin/gateway/tenants/{id}/suspend", m.adminSuspendTenant)

	mux.HandleFunc("POST /admin/gateway/tenants/{id}/lifecycle/propose", m.adminProposeLifecycle)
	mux.HandleFunc("GET /admin/gateway/tenants/{id}/lifecycle", m.adminListLifecycle)
	mux.HandleFunc("POST /admin/gateway/lifecycle/{id}/approve", m.adminDecideLifecycle("approve"))
	mux.HandleFunc("POST /admin/gateway/lifecycle/{id}/reject", m.adminDecideLifecycle("reject"))

	mux.HandleFunc("POST /admin/gateway/tenants/{id}/account", m.adminProvisionAccount)
	mux.HandleFunc("GET /admin/gateway/tenants/{id}/account", m.adminGetAccount)

	mux.HandleFunc("POST /admin/gateway/tenants/{id}/keys", m.adminCreateKey)
	mux.HandleFunc("GET /admin/gateway/tenants/{id}/keys", m.adminListKeys)
	mux.HandleFunc("POST /admin/gateway/tenants/{id}/keys/{keyID}/rotate", m.adminRotateKey)
	mux.HandleFunc("POST /admin/gateway/tenants/{id}/keys/{keyID}/revoke", m.adminRevokeKey)

	mux.HandleFunc("GET /admin/gateway/tenants/{id}/quota", m.adminGetQuota)
	mux.HandleFunc("PUT /admin/gateway/tenants/{id}/quota", m.adminUpdateQuota)

	mux.HandleFunc("POST /admin/gateway/tenants/{id}/webhooks", m.adminCreateWebhookEndpoint)
	mux.HandleFunc("GET /admin/gateway/tenants/{id}/webhooks", m.adminListWebhookEndpoints)
	mux.HandleFunc("POST /admin/gateway/tenants/{id}/webhooks/{whID}/rotate-secret", m.adminRotateWebhookSecret)
	mux.HandleFunc("POST /admin/gateway/tenants/{id}/webhooks/{whID}/disable", m.adminDisableWebhookEndpoint)
	mux.HandleFunc("GET /admin/gateway/tenants/{id}/deliveries", m.adminListDeliveries)
	mux.HandleFunc("POST /admin/gateway/tenants/{id}/deliveries/{deliveryID}/replay", m.adminReplayDelivery)

	mux.HandleFunc("GET /admin/gateway/global/b2b-api", m.adminGetGlobalFlag)
	mux.HandleFunc("PUT /admin/gateway/global/b2b-api", m.adminSetGlobalFlag)

	return mux
}

// ─── Role gates (T8 acceptance: "unauthorized roles cannot view or mutate
// merchant management") — byte-identical convention to
// internal/ledger/transport/http.go's own isAdmin/isAdminMaker/
// isAdminChecker (this codebase duplicates this trio per package rather
// than sharing one, an established precedent — see
// internal/auth/operator_offboarding_http.go's own isAdminMaker/
// isAdminChecker for the second independent copy).

func isAdmin(r *http.Request) bool {
	claims := middleware.GetClaims(r.Context())
	return claims != nil && (claims.Role == "admin" || claims.Role == "admin_maker" || claims.Role == "admin_checker")
}

func isAdminMaker(r *http.Request) bool {
	claims := middleware.GetClaims(r.Context())
	return claims != nil && (claims.Role == "admin" || claims.Role == "admin_maker")
}

func isAdminChecker(r *http.Request) bool {
	claims := middleware.GetClaims(r.Context())
	return claims != nil && (claims.Role == "admin" || claims.Role == "admin_checker")
}

// actorFromClaims is the operator identity recorded in created_by/
// requested_by/approved_by columns — email when present, falling back to
// the JWT subject, matching internal/auth's own "operator identity (email
// or admin user id)" convention.
func actorFromClaims(r *http.Request) string {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		return ""
	}
	if claims.Email != "" {
		return claims.Email
	}
	return claims.UserID
}

func pathUUID(w http.ResponseWriter, r *http.Request, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(param))
	if err != nil {
		response.BadRequest(w, "invalid "+param)
		return uuid.Nil, false
	}
	return id, true
}

func mapNotFound(w http.ResponseWriter, err error) {
	if err == repository.ErrNotFound {
		response.NotFound(w, "not found")
		return
	}
	response.InternalServerError(w, err)
}

// writeKeyServiceError maps KeyService's own typed validation errors to
// 400/409 — an operator submitting an unknown scope or exceeding the
// active-key limit gets a clear client error, not a bare 500 (found live:
// an unknown-scope request initially fell through to
// response.InternalServerError before this helper existed).
func writeKeyServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUnknownScope), errors.Is(err, auth.ErrEnvironmentMismatch):
		response.BadRequest(w, err.Error())
	case errors.Is(err, auth.ErrTooManyActiveKeys):
		response.Conflict(w, err.Error())
	case errors.Is(err, repository.ErrNotFound):
		response.NotFound(w, "not found")
	default:
		response.InternalServerError(w, err)
	}
}

// writeWebhookServiceError maps webhook.Service's own validation errors to
// 400 — found live during T10's final verification pass:
// adminCreateWebhookEndpoint previously sent every CreateEndpoint error,
// including a plain bad "environment" value or an empty subscribed-events
// list, straight to response.InternalServerError, the same class of bug
// writeKeyServiceError above already exists to prevent for API keys.
func writeWebhookServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, webhook.ErrTenantRequired),
		errors.Is(err, webhook.ErrURLRequired),
		errors.Is(err, webhook.ErrInvalidURL),
		errors.Is(err, webhook.ErrInvalidEnvironment),
		errors.Is(err, webhook.ErrEventsRequired):
		response.BadRequest(w, err.Error())
	case errors.Is(err, repository.ErrNotFound):
		response.NotFound(w, "not found")
	default:
		response.InternalServerError(w, err)
	}
}

// ─── Tenants ────────────────────────────────────────────────────────────

type createTenantRequest struct {
	ExternalCode    string `json:"external_code"`
	Name            string `json:"name"`
	Environment     string `json:"environment"`
	DefaultCurrency string `json:"default_currency"`
}

// adminCreateTenant is maker-only (§16.3: "sandbox tenant creation:
// maker") — a sandbox tenant is created directly 'active' (no checker
// gate exists for sandbox at all); a live tenant is created 'draft' and
// requires a separate lifecycle propose/approve to reach 'active'
// (§16.2: "create live-mode tenant in draft state").
func (m *Module) adminCreateTenant(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	var req createTenantRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.ExternalCode == "" || req.Name == "" || req.DefaultCurrency == "" {
		response.BadRequest(w, "external_code, name, and default_currency are required")
		return
	}
	if req.Environment != "sandbox" && req.Environment != "live" {
		response.BadRequest(w, "environment must be sandbox or live")
		return
	}

	status := "draft"
	if req.Environment == "sandbox" {
		status = "active"
	}
	actor := actorFromClaims(r)
	tenant := model.Tenant{
		ID: generalutil.NewV7(), PublicID: "tn_" + uuid.NewString()[:16], ExternalCode: req.ExternalCode,
		Name: req.Name, Environment: req.Environment, Status: status, DefaultCurrency: req.DefaultCurrency,
		CreatedBy: actor,
	}
	if err := m.Tenants.Create(r.Context(), tenant); err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.Created(w, tenant)
}

func (m *Module) adminListTenants(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	// TenantRepository has no ListAll — T8's scope is per-tenant detail
	// pages driven from a known id/public_id (§16.2 lists "tenant list and
	// detail" but does not require a bare unfiltered scan of every tenant
	// ever provisioned); look up by external_code or public_id via the
	// query string instead.
	if code := r.URL.Query().Get("public_id"); code != "" {
		tenant, err := m.Tenants.GetByPublicID(r.Context(), code)
		if err != nil {
			mapNotFound(w, err)
			return
		}
		response.OK(w, []model.Tenant{tenant})
		return
	}
	response.OK(w, []model.Tenant{})
}

func (m *Module) adminGetTenant(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	tenant, err := m.Tenants.GetByID(r.Context(), id)
	if err != nil {
		mapNotFound(w, err)
		return
	}
	response.OK(w, tenant)
}

// adminSuspendTenant has no checker gate — §16.3 lists a checker
// requirement for activation and closure only; suspension (a reversible,
// immediately-protective action) stays maker-only.
func (m *Module) adminSuspendTenant(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := m.Tenants.UpdateStatus(r.Context(), id, "suspended", actorFromClaims(r)); err != nil {
		mapNotFound(w, err)
		return
	}
	response.NoContent(w)
}

// ─── Tenant lifecycle (maker-checker: live activation, closure) ─────────

type proposeLifecycleRequest struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

func (m *Module) adminProposeLifecycle(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req proposeLifecycleRequest
	if !response.Decode(w, r, &req) {
		return
	}
	result, err := m.LifecycleService.Propose(r.Context(), tenantID, req.Action, actorFromClaims(r), req.Reason)
	if err != nil {
		writeLifecycleError(w, err)
		return
	}
	response.Created(w, result)
}

func (m *Module) adminListLifecycle(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	list, err := m.LifecycleService.List(r.Context(), tenantID, status, 50)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, list)
}

func (m *Module) adminDecideLifecycle(decision string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAdminChecker(r) {
			response.Forbidden(w, "checker privileges required")
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		var result model.TenantLifecycleRequest
		var err error
		if decision == "approve" {
			result, err = m.LifecycleService.Approve(r.Context(), id, actorFromClaims(r))
		} else {
			result, err = m.LifecycleService.Reject(r.Context(), id, actorFromClaims(r))
		}
		if err != nil {
			writeLifecycleError(w, err)
			return
		}
		response.OK(w, result)
	}
}

func writeLifecycleError(w http.ResponseWriter, err error) {
	switch err {
	case lifecycle.ErrInvalidAction:
		response.BadRequest(w, err.Error())
	case lifecycle.ErrNotFound, repository.ErrNotFound:
		response.NotFound(w, "not found")
	case lifecycle.ErrSelfApproval:
		response.Forbidden(w, err.Error())
	case lifecycle.ErrAlreadyDecided:
		response.Conflict(w, err.Error())
	default:
		response.InternalServerError(w, err)
	}
}

// ─── Account provisioning ────────────────────────────────────────────────

func (m *Module) adminProvisionAccount(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	if m.Ledger == nil {
		response.ServiceUnavailable(w, "LEDGER_UNAVAILABLE", "ledger client is not configured")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	tenant, err := m.Tenants.GetByID(r.Context(), tenantID)
	if err != nil {
		mapNotFound(w, err)
		return
	}
	accountID, err := m.Ledger.ProvisionMerchant(r.Context(), tenantID, tenant.DefaultCurrency)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	if err := m.Tenants.SetPrimaryAccount(r.Context(), tenantID, accountID); err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.Created(w, map[string]any{"account_id": accountID})
}

func (m *Module) adminGetAccount(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	if m.Ledger == nil {
		response.ServiceUnavailable(w, "LEDGER_UNAVAILABLE", "ledger client is not configured")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	account, err := m.Ledger.GetMerchantAccount(r.Context(), tenantID)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, account)
}

// ─── API keys (§16.4: secret plaintext appears only in this immediate
// create/rotate response, is never re-fetchable, never stored in audit
// details, masked from logs/templates after the response) ───────────────

type createKeyRequest struct {
	Environment string   `json:"environment"`
	Scopes      []string `json:"scopes"`
}

func (m *Module) adminCreateKey(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req createKeyRequest
	if !response.Decode(w, r, &req) {
		return
	}
	plaintext, keyID, err := m.KeyService.CreateKey(r.Context(), tenantID, req.Environment, req.Scopes, actorFromClaims(r))
	if err != nil {
		writeKeyServiceError(w, err)
		return
	}
	// The one and only response that ever carries plaintext — see this
	// handler's own §16.4 doc comment above.
	response.Created(w, map[string]any{"key_id": keyID, "plaintext": plaintext})
}

func (m *Module) adminListKeys(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	keys, err := m.APIKeys.ListByTenant(r.Context(), tenantID)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	// SecretDigest never leaves this process — redactedKey below is the
	// entire wire shape, not model.APIKey itself.
	out := make([]redactedKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, redactedKey{
			ID: k.ID, PublicID: k.PublicID, PublicPrefix: k.PublicPrefix, Environment: k.Environment,
			Status: k.Status, Scopes: k.Scopes, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt,
		})
	}
	response.OK(w, out)
}

type redactedKey struct {
	ID           uuid.UUID  `json:"id"`
	PublicID     string     `json:"public_id"`
	PublicPrefix string     `json:"public_prefix"`
	Environment  string     `json:"environment"`
	Status       string     `json:"status"`
	Scopes       []string   `json:"scopes"`
	CreatedAt    time.Time  `json:"created_at"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
}

func (m *Module) adminRotateKey(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	oldKeyID, ok := pathUUID(w, r, "keyID")
	if !ok {
		return
	}
	var req createKeyRequest
	if !response.Decode(w, r, &req) {
		return
	}
	plaintext, newKeyID, err := m.KeyService.RotateKey(r.Context(), tenantID, oldKeyID, req.Environment, req.Scopes, actorFromClaims(r))
	if err != nil {
		writeKeyServiceError(w, err)
		return
	}
	response.Created(w, map[string]any{"key_id": newKeyID, "plaintext": plaintext})
}

func (m *Module) adminRevokeKey(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := pathUUID(w, r, "keyID")
	if !ok {
		return
	}
	if err := m.APIKeys.Revoke(r.Context(), tenantID, keyID, actorFromClaims(r)); err != nil {
		mapNotFound(w, err)
		return
	}
	response.NoContent(w)
}

// ─── Quota (§16.3: "quota increase above the default baseline: checker")

type updateQuotaRequest struct {
	QuotaClass        string `json:"quota_class"`
	RequestsPerMinute int    `json:"requests_per_minute"`
	Burst             int    `json:"burst"`
	IsEnabled         bool   `json:"is_enabled"`
}

func (m *Module) adminGetQuota(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	class := r.URL.Query().Get("quota_class")
	if class == "" {
		class = "default"
	}
	policy, err := m.Quotas.GetByTenantAndClass(r.Context(), tenantID, class)
	if err != nil {
		mapNotFound(w, err)
		return
	}
	response.OK(w, policy)
}

// adminUpdateQuota requires the checker role whenever the REQUESTED
// values exceed the baseline — a maker may freely set or lower a quota at
// or below baseline, but raising either number above it needs a checker,
// structurally: the comparison happens before ANY write, so a maker can
// never sneak a raise through by, say, requesting isEnabled changes
// alongside an oversized limit.
func (m *Module) adminUpdateQuota(w http.ResponseWriter, r *http.Request) {
	var req updateQuotaRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.QuotaClass == "" {
		req.QuotaClass = "default"
	}
	aboveBaseline := req.RequestsPerMinute > defaultQuotaBaselineRPM || req.Burst > defaultQuotaBaselineBurst
	if aboveBaseline {
		if !isAdminChecker(r) {
			response.Forbidden(w, "checker privileges required to raise quota above the default baseline")
			return
		}
	} else if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	policy := model.QuotaPolicy{
		TenantID: tenantID, QuotaClass: req.QuotaClass, RequestsPerMinute: req.RequestsPerMinute,
		Burst: req.Burst, IsEnabled: req.IsEnabled,
	}
	if err := m.Quotas.Upsert(r.Context(), policy); err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, policy)
}

// ─── Webhook endpoints/deliveries/replay (T7's own service, exposed here
// for operator use — §16.2's "inspect webhook endpoints", "disable an
// endpoint", "inspect delivery attempts", "replay eligible delivery") ────

type createWebhookEndpointRequest struct {
	URL              string   `json:"url"`
	Environment      string   `json:"environment"`
	SubscribedEvents []string `json:"subscribed_events"`
	Description      *string  `json:"description,omitempty"`
}

func (m *Module) adminCreateWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req createWebhookEndpointRequest
	if !response.Decode(w, r, &req) {
		return
	}
	endpoint, secret, err := m.WebhookService.CreateEndpoint(r.Context(), tenantID, req.URL, req.Environment, req.SubscribedEvents, req.Description)
	if err != nil {
		writeWebhookServiceError(w, err)
		return
	}
	// One-time plaintext secret — see the api-keys section's own §16.4 doc
	// comment; identical contract for webhook endpoint secrets.
	response.Created(w, map[string]any{"endpoint": redactWebhookEndpoint(endpoint), "plaintext_secret": secret})
}

func (m *Module) adminListWebhookEndpoints(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	endpoints, err := m.WebhookService.ListEndpoints(r.Context(), tenantID)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	out := make([]redactedWebhookEndpoint, 0, len(endpoints))
	for _, e := range endpoints {
		out = append(out, redactWebhookEndpoint(e))
	}
	response.OK(w, out)
}

type redactedWebhookEndpoint struct {
	ID               uuid.UUID  `json:"id"`
	PublicID         string     `json:"public_id"`
	URL              string     `json:"url"`
	Status           string     `json:"status"`
	Environment      string     `json:"environment"`
	SubscribedEvents []string   `json:"subscribed_events"`
	Description      *string    `json:"description,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	DisabledAt       *time.Time `json:"disabled_at,omitempty"`
}

func redactWebhookEndpoint(e model.WebhookEndpoint) redactedWebhookEndpoint {
	return redactedWebhookEndpoint{
		ID: e.ID, PublicID: e.PublicID, URL: e.URL, Status: e.Status, Environment: e.Environment,
		SubscribedEvents: e.SubscribedEvents, Description: e.Description, CreatedAt: e.CreatedAt, DisabledAt: e.DisabledAt,
	}
}

func (m *Module) adminRotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	endpointID, ok := pathUUID(w, r, "whID")
	if !ok {
		return
	}
	secret, err := m.WebhookService.RotateSecret(r.Context(), tenantID, endpointID)
	if err != nil {
		mapNotFound(w, err)
		return
	}
	response.OK(w, map[string]any{"plaintext_secret": secret})
}

type disableEndpointRequest struct {
	Reason string `json:"reason"`
}

// adminDisableWebhookEndpoint is "maker with reason" (§16.3) — no checker
// gate, but a reason is mandatory so the audit trail explains why an
// operator force-disabled a merchant's endpoint.
func (m *Module) adminDisableWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	endpointID, ok := pathUUID(w, r, "whID")
	if !ok {
		return
	}
	var req disableEndpointRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.Reason == "" {
		response.BadRequest(w, "reason is required")
		return
	}
	if err := m.WebhookService.SetEndpointStatus(r.Context(), tenantID, endpointID, "disabled"); err != nil {
		mapNotFound(w, err)
		return
	}
	response.NoContent(w)
}

func (m *Module) adminListDeliveries(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	deliveries, err := m.WebhookService.ListDeliveries(r.Context(), tenantID, 50)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, deliveries)
}

type replayDeliveryRequest struct {
	Reason string `json:"reason"`
}

// adminReplayDelivery is "maker with reason" (§16.3) — same posture as
// disable above.
func (m *Module) adminReplayDelivery(w http.ResponseWriter, r *http.Request) {
	if !isAdminMaker(r) {
		response.Forbidden(w, "maker privileges required")
		return
	}
	tenantID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	deliveryID, ok := pathUUID(w, r, "deliveryID")
	if !ok {
		return
	}
	var req replayDeliveryRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.Reason == "" {
		response.BadRequest(w, "reason is required")
		return
	}
	replay, err := m.WebhookService.Replay(r.Context(), tenantID, deliveryID)
	if err != nil {
		mapNotFound(w, err)
		return
	}
	response.Created(w, replay)
}

// ─── Global route-disable control (T9) — an incident-response kill switch
// for the ENTIRE merchant B2B API surface, independent of any single
// tenant's own suspension. See internal/merchant/auth.GlobalFlag's own
// doc comment for the enforcement side.

func (m *Module) adminGetGlobalFlag(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		response.Forbidden(w, "admin privileges required")
		return
	}
	response.OK(w, map[string]any{"b2b_api_enabled": m.GlobalFlag.Enabled()})
}

type setGlobalFlagRequest struct {
	Enabled bool `json:"enabled"`
}

// adminSetGlobalFlag requires the checker role — disabling (or
// re-enabling) the entire merchant B2B API for every tenant at once is
// the single highest-blast-radius action this router exposes, more so
// than any single tenant's own closure.
func (m *Module) adminSetGlobalFlag(w http.ResponseWriter, r *http.Request) {
	if !isAdminChecker(r) {
		response.Forbidden(w, "checker privileges required")
		return
	}
	var req setGlobalFlagRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if err := m.GlobalFlag.SetEnabled(r.Context(), req.Enabled, actorFromClaims(r)); err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, map[string]any{"b2b_api_enabled": req.Enabled})
}
