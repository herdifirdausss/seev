// Package auth is the stable public facade for the Auth bounded context.
// Business decisions live in internal/auth; repositories, workers, KYC adapters,
// and HTTP transport are kept in their own packages below this module.
package auth

import (
	"log/slog"
	"net/http"

	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/services/auth/internal/adapter/kycvendor"
	authinternal "github.com/herdifirdausss/seev/services/auth/internal/auth"
	authhandler "github.com/herdifirdausss/seev/services/auth/internal/transport/http"
)

var (
	ErrEmailTaken                        = authinternal.ErrEmailTaken
	ErrInvalidCredentials                = authinternal.ErrInvalidCredentials
	ErrUserDisabled                      = authinternal.ErrUserDisabled
	ErrInvalidRefreshToken               = authinternal.ErrInvalidRefreshToken
	ErrValidation                        = authinternal.ErrValidation
	ErrKYCLevelSequence                  = authinternal.ErrKYCLevelSequence
	ErrKYCPending                        = authinternal.ErrKYCPending
	ErrKYCProvider                       = authinternal.ErrKYCProvider
	ErrKYCApplyQueued                    = authinternal.ErrKYCApplyQueued
	ErrDocumentStorageUnavailable        = authinternal.ErrDocumentStorageUnavailable
	ErrDocumentInvalid                   = authinternal.ErrDocumentInvalid
	ErrClosureUnavailable                = authinternal.ErrClosureUnavailable
	ErrClosureOwnerUnavailable           = authinternal.ErrClosureOwnerUnavailable
	ErrClosureNotSelfService             = authinternal.ErrClosureNotSelfService
	ErrClosureNotFound                   = authinternal.ErrClosureNotFound
	ErrExportStorageUnavailable          = authinternal.ErrExportStorageUnavailable
	ErrExportNotFound                    = authinternal.ErrExportNotFound
	ErrExportActiveAlready               = authinternal.ErrExportActiveAlready
	ErrExportNotReady                    = authinternal.ErrExportNotReady
	ErrExportAlreadyDownloaded           = authinternal.ErrExportAlreadyDownloaded
	ErrExportExpired                     = authinternal.ErrExportExpired
	ErrOperatorOffboardingNotFound       = authinternal.ErrOperatorOffboardingNotFound
	ErrOperatorOffboardingNotOperator    = authinternal.ErrOperatorOffboardingNotOperator
	ErrOperatorOffboardingSelfApproval   = authinternal.ErrOperatorOffboardingSelfApproval
	ErrOperatorOffboardingAlreadyDecided = authinternal.ErrOperatorOffboardingAlreadyDecided
)

type (
	AdminPrivacyRequest         = authinternal.AdminPrivacyRequest
	Config                      = authinternal.Config
	DocumentStore               = authinternal.DocumentStore
	ExecutionSubjectProvisioner = authinternal.ExecutionSubjectProvisioner
	FileDocumentStore           = authinternal.FileDocumentStore
	KYCApplyQueuedError         = authinternal.KYCApplyQueuedError
	KYCStatus                   = authinternal.KYCStatus
	Module                      struct{ *authinternal.Module }
	OperatorOffboardingRequest  = authinternal.OperatorOffboardingRequest
	OwnerClosureClient          = authinternal.OwnerClosureClient
	PrivacyRequest              = authinternal.PrivacyRequest
	Provisioner                 = authinternal.Provisioner
	TokenPair                   = authinternal.TokenPair
	User                        = authinternal.User
)

func NewModule(db database.DatabaseSQL, provisioner Provisioner, cfg Config, logger *slog.Logger, ring *cryptox.Ring, lookup *cryptox.LookupKey, providers ...kycvendor.Provider) *Module {
	return &Module{Module: authinternal.NewModule(db, provisioner, cfg, logger, ring, lookup, providers...)}
}

func NewFileDocumentStore(root string) (*FileDocumentStore, error) {
	return authinternal.NewFileDocumentStore(root)
}

func NewOwnerClosureClient(baseURL, internalToken string, httpClient *http.Client) OwnerClosureClient {
	return authinternal.NewOwnerClosureClient(baseURL, internalToken, httpClient)
}

func (m *Module) RegisterHandler() http.HandlerFunc {
	return authhandler.New(m.Module).RegisterHandler()
}
func (m *Module) LoginHandler() http.HandlerFunc   { return authhandler.New(m.Module).LoginHandler() }
func (m *Module) RefreshHandler() http.HandlerFunc { return authhandler.New(m.Module).RefreshHandler() }
func (m *Module) MeHandler() http.HandlerFunc      { return authhandler.New(m.Module).MeHandler() }
func (m *Module) UpdateMeHandler() http.HandlerFunc {
	return authhandler.New(m.Module).UpdateMeHandler()
}
func (m *Module) SubmitKYCHandler() http.HandlerFunc {
	return authhandler.New(m.Module).SubmitKYCHandler()
}
func (m *Module) KYCStatusHandler() http.HandlerFunc {
	return authhandler.New(m.Module).KYCStatusHandler()
}
func (m *Module) UploadKYCDocumentHandler() http.HandlerFunc {
	return authhandler.New(m.Module).UploadKYCDocumentHandler()
}
func (m *Module) AdminDownloadKYCDocumentHandler() http.HandlerFunc {
	return authhandler.New(m.Module).AdminDownloadKYCDocumentHandler()
}
func (m *Module) AdminListKYCHandler() http.HandlerFunc {
	return authhandler.New(m.Module).AdminListKYCHandler()
}
func (m *Module) AdminApproveKYCHandler() http.HandlerFunc {
	return authhandler.New(m.Module).AdminApproveKYCHandler()
}
func (m *Module) AdminDowngradeKYCHandler() http.HandlerFunc {
	return authhandler.New(m.Module).AdminDowngradeKYCHandler()
}
func (m *Module) AdminRejectKYCHandler() http.HandlerFunc {
	return authhandler.New(m.Module).AdminRejectKYCHandler()
}
func (m *Module) CreateClosureHandler() http.HandlerFunc {
	return authhandler.New(m.Module).CreateClosureHandler()
}
func (m *Module) ClosureStatusHandler() http.HandlerFunc {
	return authhandler.New(m.Module).ClosureStatusHandler()
}
func (m *Module) CreateExportHandler() http.HandlerFunc {
	return authhandler.New(m.Module).CreateExportHandler()
}
func (m *Module) ExportStatusHandler() http.HandlerFunc {
	return authhandler.New(m.Module).ExportStatusHandler()
}
func (m *Module) DownloadExportHandler() http.HandlerFunc {
	return authhandler.New(m.Module).DownloadExportHandler()
}
func (m *Module) AdminProposeOperatorOffboardingHandler() http.HandlerFunc {
	return authhandler.New(m.Module).AdminProposeOperatorOffboardingHandler()
}
func (m *Module) AdminApproveOperatorOffboardingHandler() http.HandlerFunc {
	return authhandler.New(m.Module).AdminApproveOperatorOffboardingHandler()
}
func (m *Module) AdminRejectOperatorOffboardingHandler() http.HandlerFunc {
	return authhandler.New(m.Module).AdminRejectOperatorOffboardingHandler()
}
func (m *Module) AdminListOperatorOffboardingHandler() http.HandlerFunc {
	return authhandler.New(m.Module).AdminListOperatorOffboardingHandler()
}
func (m *Module) NotificationContactHandler() http.HandlerFunc {
	return authhandler.New(m.Module).NotificationContactHandler()
}
func (m *Module) AdminPrivacyRequestsHandler() http.HandlerFunc {
	return authhandler.New(m.Module).AdminPrivacyRequestsHandler()
}
