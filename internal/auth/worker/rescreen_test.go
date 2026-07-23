package worker

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

type rescreenRepoFake struct {
	repository.KYCRepository
	subjects []model.KYCRescreenSubject
}

func (f *rescreenRepoFake) ListKYCRescreenSubjects(context.Context, int) ([]model.KYCRescreenSubject, error) {
	return f.subjects, nil
}

type rescreenCheckerFake struct {
	calls []model.KYCRescreenSubject
}

func (f *rescreenCheckerFake) CheckWithSubject(_ context.Context, flow, txType string, userID uuid.UUID, amount decimal.Decimal, currency, name, birthDate string) (fraudcheck.Verdict, error) {
	if flow != "kyc_rescreen" || txType != "kyc" || amount.String() != "1" || currency != "IDR" {
		return fraudcheck.Verdict{}, requireErr("unexpected screening contract")
	}
	f.calls = append(f.calls, model.KYCRescreenSubject{UserID: userID, Name: name, BirthDate: birthDate})
	return fraudcheck.Verdict{Block: true, Reason: "fixture match"}, nil
}

type requireError string

func (e requireError) Error() string { return string(e) }

func requireErr(message string) error { return requireError(message) }

func TestRescreenJobRunOnceScreensApprovedSubjectsWithoutMutation(t *testing.T) {
	subject := model.KYCRescreenSubject{UserID: uuid.New(), Name: "Jane Doe", BirthDate: "1980-01-02"}
	repo := &rescreenRepoFake{subjects: []model.KYCRescreenSubject{subject}}
	checker := &rescreenCheckerFake{}
	lock := scheduler.NewMemoryLock(time.Minute)
	defer lock.Stop()

	job := NewRescreenJob(repo, checker, lock, "IDR", time.Hour, nil)
	require.NoError(t, job.RunOnce(context.Background()))
	require.Equal(t, []model.KYCRescreenSubject{subject}, checker.calls)
}

func TestRescreenJobRejectsMissingDependencies(t *testing.T) {
	job := NewRescreenJob(nil, nil, nil, "IDR", time.Hour, nil)
	require.Error(t, job.RunOnce(context.Background()))
}
