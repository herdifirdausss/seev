// Package mockkyc is a deterministic KYC provider for local development and
// integration tests. L2 always requires manual review by design.
package mockkyc

import (
	"context"
	"errors"
	"fmt"

	"github.com/herdifirdausss/seev/internal/kycvendor"
)

const ProviderName = "mockkyc"

const (
	ModeApprove = "approve"
	ModeReject  = "reject"
	ModeRefer   = "refer"
	ModeTimeout = "timeout"
)

var ErrTimeout = errors.New("mockkyc: provider timeout")

type Provider struct{}

func New() *Provider { return &Provider{} }

func (p *Provider) Name() string { return ProviderName }

func (p *Provider) Verify(_ context.Context, submission kycvendor.Submission) (kycvendor.Decision, error) {
	if submission.LevelRequested == 2 {
		return kycvendor.Decision{
			Verdict: kycvendor.VerdictRefer,
			Ref:     fmt.Sprintf("%s-%d", ProviderName, submission.LevelRequested),
			Reason:  "level 2 requires manual review",
		}, nil
	}

	mode := ""
	if submission.Payload != nil {
		if value, ok := submission.Payload["mock_mode"].(string); ok {
			mode = value
		}
	}
	if mode == "" {
		mode = ModeApprove
	}

	switch mode {
	case ModeApprove:
		return kycvendor.Decision{Verdict: kycvendor.VerdictApprove, Ref: ProviderName + "-approved"}, nil
	case ModeReject:
		return kycvendor.Decision{Verdict: kycvendor.VerdictReject, Ref: ProviderName + "-rejected", Reason: "mock verification rejected"}, nil
	case ModeRefer:
		return kycvendor.Decision{Verdict: kycvendor.VerdictRefer, Ref: ProviderName + "-referred", Reason: "manual review required"}, nil
	case ModeTimeout:
		return kycvendor.Decision{}, ErrTimeout
	default:
		return kycvendor.Decision{}, fmt.Errorf("mockkyc: unsupported mock_mode %q", mode)
	}
}
