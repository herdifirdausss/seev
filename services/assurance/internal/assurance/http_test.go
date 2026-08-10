package assurance

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	payinv1 "github.com/herdifirdausss/seev/gen/go/payin/v1"
	payoutv1 "github.com/herdifirdausss/seev/gen/go/payout/v1"
	"github.com/herdifirdausss/seev/internal/platform/config"
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/security/middleware"
)

const assuranceTestSecret = "assurance-test-secret-at-least-32-bytes"

func newAssuranceHTTPTestModule(t *testing.T, payin PayinReader, payout PayoutReader) (*Module, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	db := database.NewFromSQL(sqlDB, database.Config{})
	return NewModule(db, config.AssuranceConfig{}, payin, payout, nil, nil, nil), mock
}

func serveAssurance(t *testing.T, module *Module, role, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	token, err := middleware.GenerateToken(assuranceTestSecret, middleware.Claims{
		UserID: "operator-1", Email: "operator@example.test", Role: role,
		Exp: time.Now().Add(time.Hour).Unix(), Iss: "assurance-test",
	})
	require.NoError(t, err)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	middleware.WithAuth(assuranceTestSecret, "assurance-test")(module.AdminRouter()).ServeHTTP(recorder, req)
	return recorder
}

func TestAssuranceAdminReadJourneys(t *testing.T) {
	t.Run("summary", func(t *testing.T) {
		module, mock := newAssuranceHTTPTestModule(t, nil, nil)
		mock.ExpectQuery(`SELECT severity, status, COUNT\(\*\)`).WillReturnRows(
			sqlmock.NewRows([]string{"severity", "status", "count", "amount"}).AddRow("high", "open", int64(2), int64(500)))

		response := serveAssurance(t, module, "admin", http.MethodGet, "/admin/assurance/summary", "")
		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Body.String(), `"severity":"high"`)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("filtered findings", func(t *testing.T) {
		module, mock := newAssuranceHTTPTestModule(t, nil, nil)
		now := time.Now().UTC()
		mock.ExpectQuery(`SELECT id, fingerprint, severity, rule_code`).WithArgs("open", 2).WillReturnRows(
			sqlmock.NewRows([]string{"id", "fingerprint", "severity", "rule_code", "resource_id", "amount_minor", "currency", "evidence", "first_seen_at", "last_seen_at", "occurrence_count", "status"}).
				AddRow(uuid.New().String(), "fp", "high", "PAYIN_MISSING", "resource-1", int64(100), "IDR", []byte(`{"source":"payin"}`), now, now, int64(1), "open"))

		response := serveAssurance(t, module, "admin_checker", http.MethodGet, "/admin/assurance/findings?status=open&limit=2", "")
		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Body.String(), `"rule_code":"PAYIN_MISSING"`)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("runs", func(t *testing.T) {
		module, mock := newAssuranceHTTPTestModule(t, nil, nil)
		now := time.Now().UTC()
		mock.ExpectQuery(`SELECT id, mode, status, baseline`).WillReturnRows(
			sqlmock.NewRows([]string{"id", "mode", "status", "baseline", "cutoff_at", "started_at", "finished_at", "records_scanned", "pages_scanned", "findings_opened", "error_code"}).
				AddRow(uuid.New().String(), "manual", "succeeded", false, now, now, now, 3, 1, 1, ""))

		response := serveAssurance(t, module, "admin_maker", http.MethodGet, "/admin/assurance/runs", "")
		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Body.String(), `"status":"succeeded"`)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAssuranceAdminRejectsUnauthorizedAndInvalidInput(t *testing.T) {
	module, mock := newAssuranceHTTPTestModule(t, nil, nil)

	require.Equal(t, http.StatusForbidden,
		serveAssurance(t, module, "user", http.MethodGet, "/admin/assurance/summary", "").Code)
	require.Equal(t, http.StatusBadRequest,
		serveAssurance(t, module, "admin", http.MethodGet, "/admin/assurance/findings?limit=201", "").Code)
	require.Equal(t, http.StatusBadRequest,
		serveAssurance(t, module, "admin", http.MethodPost, "/admin/assurance/findings/not-a-uuid/acknowledge", `{"reason":"checked"}`).Code)
	require.Equal(t, http.StatusBadRequest,
		serveAssurance(t, module, "admin", http.MethodPost, "/admin/assurance/findings/"+uuid.NewString()+"/resolve", `{}`).Code)
	require.Equal(t, http.StatusBadRequest,
		serveAssurance(t, module, "admin_maker", http.MethodPost, "/admin/assurance/intake/unknown/pause", `{"command_id":"`+uuid.NewString()+`","reason":"incident"}`).Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAssuranceAdminMapsDependencyAndPersistenceFailures(t *testing.T) {
	t.Run("read failures", func(t *testing.T) {
		for _, testCase := range []struct {
			name, query, target string
		}{
			{name: "summary", query: `SELECT severity, status`, target: "/admin/assurance/summary"},
			{name: "findings", query: `SELECT id, fingerprint`, target: "/admin/assurance/findings"},
			{name: "runs", query: `SELECT id, mode`, target: "/admin/assurance/runs"},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				module, mock := newAssuranceHTTPTestModule(t, nil, nil)
				mock.ExpectQuery(testCase.query).WillReturnError(errors.New("database unavailable"))
				response := serveAssurance(t, module, "admin", http.MethodGet, testCase.target, "")
				require.Equal(t, http.StatusServiceUnavailable, response.Code)
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	})

	t.Run("finding not found", func(t *testing.T) {
		module, mock := newAssuranceHTTPTestModule(t, nil, nil)
		findingID := uuid.New()
		mock.ExpectExec(`UPDATE assurance_findings`).WillReturnResult(sqlmock.NewResult(0, 0))
		response := serveAssurance(t, module, "admin", http.MethodPost,
			"/admin/assurance/findings/"+findingID.String()+"/resolve", `{"reason":"no matching record"}`)
		require.Equal(t, http.StatusNotFound, response.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("finding persistence unavailable", func(t *testing.T) {
		module, mock := newAssuranceHTTPTestModule(t, nil, nil)
		findingID := uuid.New()
		mock.ExpectExec(`UPDATE assurance_findings`).WillReturnError(errors.New("write unavailable"))
		response := serveAssurance(t, module, "admin", http.MethodPost,
			"/admin/assurance/findings/"+findingID.String()+"/acknowledge", `{"reason":"reviewed"}`)
		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("owner unavailable", func(t *testing.T) {
		module, mock := newAssuranceHTTPTestModule(t, nil, nil)
		response := serveAssurance(t, module, "admin", http.MethodGet, "/admin/assurance/intake", "")
		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("resume self approval", func(t *testing.T) {
		module, mock := newAssuranceHTTPTestModule(t, nil, nil)
		commandID := uuid.New()
		mock.ExpectQuery(`SELECT requested_by, revision FROM intake_control_commands`).
			WithArgs(commandID, "payin").
			WillReturnRows(sqlmock.NewRows([]string{"requested_by", "revision"}).AddRow("operator-1", int64(2)))
		response := serveAssurance(t, module, "admin_checker", http.MethodPost,
			"/admin/assurance/intake/payin/resume-requests/"+commandID.String()+"/approve", "")
		require.Equal(t, http.StatusForbidden, response.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAssuranceAdminFindingMutations(t *testing.T) {
	for _, testCase := range []struct {
		name, path, status, actorField string
	}{
		{name: "acknowledge", path: "acknowledge", status: "acknowledged", actorField: "acknowledged_by"},
		{name: "resolve", path: "resolve", status: "resolved", actorField: "resolved_by"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			module, mock := newAssuranceHTTPTestModule(t, nil, nil)
			findingID := uuid.New()
			mock.ExpectExec(`UPDATE assurance_findings SET status=\$2, `+testCase.actorField).
				WithArgs(findingID, testCase.status, "operator-1").
				WillReturnResult(sqlmock.NewResult(0, 1))

			response := serveAssurance(t, module, "admin", http.MethodPost,
				"/admin/assurance/findings/"+findingID.String()+"/"+testCase.path, `{"reason":"evidence reviewed"}`)
			require.Equal(t, http.StatusOK, response.Code)
			require.Contains(t, response.Body.String(), `"status":"`+testCase.status+`"`)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

type payinControlFake struct {
	control  *payinv1.GetIntakeControlResponse
	applied  *payinv1.ApplyIntakeControlResponse
	applyErr error
}

func (f *payinControlFake) ListAssuranceRecords(context.Context, *payinv1.ListAssuranceRecordsRequest, ...grpc.CallOption) (*payinv1.ListAssuranceRecordsResponse, error) {
	return &payinv1.ListAssuranceRecordsResponse{}, nil
}
func (f *payinControlFake) GetIntakeControl(context.Context, *payinv1.GetIntakeControlRequest, ...grpc.CallOption) (*payinv1.GetIntakeControlResponse, error) {
	return f.control, nil
}
func (f *payinControlFake) ApplyIntakeControl(context.Context, *payinv1.ApplyIntakeControlRequest, ...grpc.CallOption) (*payinv1.ApplyIntakeControlResponse, error) {
	return f.applied, f.applyErr
}

type payoutControlFake struct {
	control  *payoutv1.GetIntakeControlResponse
	applied  *payoutv1.ApplyIntakeControlResponse
	applyErr error
}

func (f *payoutControlFake) ListAssuranceRecords(context.Context, *payoutv1.ListAssuranceRecordsRequest, ...grpc.CallOption) (*payoutv1.ListAssuranceRecordsResponse, error) {
	return &payoutv1.ListAssuranceRecordsResponse{}, nil
}
func (f *payoutControlFake) GetIntakeControl(context.Context, *payoutv1.GetIntakeControlRequest, ...grpc.CallOption) (*payoutv1.GetIntakeControlResponse, error) {
	return f.control, nil
}
func (f *payoutControlFake) ApplyIntakeControl(context.Context, *payoutv1.ApplyIntakeControlRequest, ...grpc.CallOption) (*payoutv1.ApplyIntakeControlResponse, error) {
	return f.applied, f.applyErr
}

func TestAssuranceAdminIntakeControlJourneys(t *testing.T) {
	payin := &payinControlFake{control: &payinv1.GetIntakeControlResponse{Paused: true, Revision: 4}, applied: &payinv1.ApplyIntakeControlResponse{Applied: true, Paused: true, Revision: 5}}
	payout := &payoutControlFake{control: &payoutv1.GetIntakeControlResponse{Paused: false, Revision: 7}, applied: &payoutv1.ApplyIntakeControlResponse{Applied: true, Paused: false, Revision: 8}}

	t.Run("read owner state", func(t *testing.T) {
		module, mock := newAssuranceHTTPTestModule(t, payin, payout)
		response := serveAssurance(t, module, "admin", http.MethodGet, "/admin/assurance/intake", "")
		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Body.String(), `"payin"`)
		require.Contains(t, response.Body.String(), `"payout"`)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("pause payin", func(t *testing.T) {
		module, mock := newAssuranceHTTPTestModule(t, payin, payout)
		commandID := uuid.New()
		mock.ExpectExec(`INSERT INTO intake_control_commands`).WithArgs(commandID, "payin", "pause", int64(4), "operator-1", "incident containment").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`UPDATE intake_control_commands SET status='applied'`).WithArgs(commandID, "operator-1", int64(5)).WillReturnResult(sqlmock.NewResult(0, 1))

		response := serveAssurance(t, module, "admin_maker", http.MethodPost, "/admin/assurance/intake/payin/pause",
			`{"command_id":"`+commandID.String()+`","expected_revision":4,"reason":"incident containment"}`)
		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Body.String(), `"revision":5`)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("maker requests resume", func(t *testing.T) {
		module, mock := newAssuranceHTTPTestModule(t, payin, payout)
		commandID := uuid.New()
		mock.ExpectExec(`INSERT INTO intake_control_commands`).WithArgs(commandID, "payout", int64(7), "operator-1", "incident cleared").WillReturnResult(sqlmock.NewResult(0, 1))

		response := serveAssurance(t, module, "admin_maker", http.MethodPost, "/admin/assurance/intake/payout/resume-requests",
			`{"command_id":"`+commandID.String()+`","expected_revision":7,"reason":"incident cleared"}`)
		require.Equal(t, http.StatusAccepted, response.Code)
		require.Contains(t, response.Body.String(), `"status":"pending"`)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("different checker approves resume", func(t *testing.T) {
		module, mock := newAssuranceHTTPTestModule(t, payin, payout)
		commandID := uuid.New()
		mock.ExpectQuery(`SELECT requested_by, revision FROM intake_control_commands`).WithArgs(commandID, "payout").WillReturnRows(sqlmock.NewRows([]string{"requested_by", "revision"}).AddRow("operator-maker", int64(7)))
		mock.ExpectExec(`UPDATE intake_control_commands SET status='applying'`).WithArgs(commandID, "operator-1").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`UPDATE intake_control_commands SET action='resume_approve'`).WithArgs(commandID, int64(8)).WillReturnResult(sqlmock.NewResult(0, 1))

		response := serveAssurance(t, module, "admin_checker", http.MethodPost, "/admin/assurance/intake/payout/resume-requests/"+commandID.String()+"/approve", "")
		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Body.String(), `"revision":8`)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("duplicate applied pause is idempotent", func(t *testing.T) {
		module, mock := newAssuranceHTTPTestModule(t, payin, payout)
		commandID := uuid.New()
		mock.ExpectExec(`INSERT INTO intake_control_commands`).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(`SELECT status FROM intake_control_commands`).WithArgs(commandID).WillReturnRows(
			sqlmock.NewRows([]string{"status"}).AddRow("applied"))

		response := serveAssurance(t, module, "admin_maker", http.MethodPost, "/admin/assurance/intake/payin/pause",
			`{"command_id":"`+commandID.String()+`","expected_revision":4,"reason":"replayed delivery"}`)
		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Body.String(), `"status":"applied"`)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("pause owner failure is durable", func(t *testing.T) {
		failedPayin := &payinControlFake{applyErr: errors.New("payin unavailable")}
		module, mock := newAssuranceHTTPTestModule(t, failedPayin, payout)
		commandID := uuid.New()
		mock.ExpectExec(`INSERT INTO intake_control_commands`).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`UPDATE intake_control_commands SET status='failed'`).WithArgs(commandID, "payin unavailable").WillReturnResult(sqlmock.NewResult(0, 1))

		response := serveAssurance(t, module, "admin_maker", http.MethodPost, "/admin/assurance/intake/payin/pause",
			`{"command_id":"`+commandID.String()+`","expected_revision":4,"reason":"incident"}`)
		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("duplicate resume request returns durable state", func(t *testing.T) {
		module, mock := newAssuranceHTTPTestModule(t, payin, payout)
		commandID := uuid.New()
		mock.ExpectExec(`INSERT INTO intake_control_commands`).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(`SELECT status FROM intake_control_commands`).WithArgs(commandID).WillReturnRows(
			sqlmock.NewRows([]string{"status"}).AddRow("pending"))

		response := serveAssurance(t, module, "admin_maker", http.MethodPost, "/admin/assurance/intake/payout/resume-requests",
			`{"command_id":"`+commandID.String()+`","expected_revision":7,"reason":"replayed request"}`)
		require.Equal(t, http.StatusAccepted, response.Code)
		require.Contains(t, response.Body.String(), `"status":"pending"`)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("missing resume request", func(t *testing.T) {
		module, mock := newAssuranceHTTPTestModule(t, payin, payout)
		commandID := uuid.New()
		mock.ExpectQuery(`SELECT requested_by, revision FROM intake_control_commands`).
			WithArgs(commandID, "payout").WillReturnError(sql.ErrNoRows)
		response := serveAssurance(t, module, "admin_checker", http.MethodPost,
			"/admin/assurance/intake/payout/resume-requests/"+commandID.String()+"/approve", "")
		require.Equal(t, http.StatusNotFound, response.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestRefreshMetricsReadsAllOperationalSeries(t *testing.T) {
	module, mock := newAssuranceHTTPTestModule(t, nil, nil)
	mock.ExpectQuery(`SELECT severity, rule_code, COUNT\(\*\)`).WillReturnRows(
		sqlmock.NewRows([]string{"severity", "rule_code", "count"}).AddRow("high", "PAYIN_MISSING", int64(2)))
	mock.ExpectQuery(`SELECT currency, COALESCE\(SUM\(amount_minor\),0\)`).WillReturnRows(
		sqlmock.NewRows([]string{"currency", "amount"}).AddRow("IDR", int64(2500)))
	mock.ExpectQuery(`SELECT source, EXTRACT`).WillReturnRows(
		sqlmock.NewRows([]string{"source", "lag"}).AddRow("payin", float64(3)))

	require.NoError(t, module.refreshMetrics(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResponseRevisionUnknownType(t *testing.T) {
	require.Zero(t, responseRevision(struct{}{}))
}

func TestAssuranceFallbackAndRunValidation(t *testing.T) {
	module, mock := newAssuranceHTTPTestModule(t, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	require.False(t, module.authorized(request, "admin"))
	require.Empty(t, actorFromRequest(request))

	_, err := module.sendOwnerCommand(context.Background(), "payin", "pause", uuid.New(), 1, "operator", "reason")
	require.Error(t, err)
	_, err = module.sendOwnerCommand(context.Background(), "payout", "pause", uuid.New(), 1, "operator", "reason")
	require.Error(t, err)

	_, err = module.Run(context.Background(), "unsupported")
	require.ErrorContains(t, err, "invalid assurance run mode")
	module.runMu.Lock()
	_, err = module.Run(context.Background(), "manual")
	module.runMu.Unlock()
	require.ErrorContains(t, err, "already active")

	response := serveAssurance(t, module, "admin_checker", http.MethodPost, "/admin/assurance/intake/payin/pause", `{}`)
	require.Equal(t, http.StatusForbidden, response.Code)
	fallback := httptest.NewRecorder()
	writeError(fallback, "unexpected", http.StatusTeapot)
	require.Equal(t, http.StatusTeapot, fallback.Code)
	require.Contains(t, fallback.Body.String(), "INTERNAL_ERROR")
	require.NoError(t, mock.ExpectationsWereMet())
}
