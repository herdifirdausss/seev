// Package http translates Auth HTTP requests into service calls. It owns
// request decoding, authentication context extraction, and response mapping;
// persistence and business decisions stay in services/auth/internal/auth.
package http

import service "github.com/herdifirdausss/seev/services/auth/internal/auth"

type Handler struct {
	module *service.Module
}

func New(module *service.Module) *Handler { return &Handler{module: module} }

type (
	AdminPrivacyRequest        = service.AdminPrivacyRequest
	FileDocumentStore          = service.FileDocumentStore
	KYCApplyQueuedError        = service.KYCApplyQueuedError
	KYCStatus                  = service.KYCStatus
	OperatorOffboardingRequest = service.OperatorOffboardingRequest
	PrivacyRequest             = service.PrivacyRequest
	TokenPair                  = service.TokenPair
	User                       = service.User
)

var (
	ErrEmailTaken                        = service.ErrEmailTaken
	ErrInvalidCredentials                = service.ErrInvalidCredentials
	ErrInvalidRefreshToken               = service.ErrInvalidRefreshToken
	ErrUserDisabled                      = service.ErrUserDisabled
	ErrValidation                        = service.ErrValidation
	ErrKYCLevelSequence                  = service.ErrKYCLevelSequence
	ErrKYCPending                        = service.ErrKYCPending
	ErrClosureNotFound                   = service.ErrClosureNotFound
	ErrClosureUnavailable                = service.ErrClosureUnavailable
	ErrClosureNotSelfService             = service.ErrClosureNotSelfService
	ErrDocumentStorageUnavailable        = service.ErrDocumentStorageUnavailable
	ErrDocumentInvalid                   = service.ErrDocumentInvalid
	ErrExportNotFound                    = service.ErrExportNotFound
	ErrExportStorageUnavailable          = service.ErrExportStorageUnavailable
	ErrExportNotReady                    = service.ErrExportNotReady
	ErrExportAlreadyDownloaded           = service.ErrExportAlreadyDownloaded
	ErrExportExpired                     = service.ErrExportExpired
	ErrOperatorOffboardingNotFound       = service.ErrOperatorOffboardingNotFound
	ErrOperatorOffboardingNotOperator    = service.ErrOperatorOffboardingNotOperator
	ErrOperatorOffboardingSelfApproval   = service.ErrOperatorOffboardingSelfApproval
	ErrOperatorOffboardingAlreadyDecided = service.ErrOperatorOffboardingAlreadyDecided
)
