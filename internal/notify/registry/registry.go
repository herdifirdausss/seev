// Package registry contains the owner-side notification policy registry.
// Database templates may change copy, but never this policy boundary.
package registry

import (
	"fmt"

	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/internal/notify/model"
)

type Kind struct {
	Kind                  string
	Category              string
	Priority              string
	Requirement           string
	AuthoritativeEvent    string
	TemplateSchemaVersion int
	DigestEligible        bool
	BypassQuietHours      bool
	DefaultModes          map[string]string
	DeepLinkPath          string
	PrivacyClass          string
	RetentionClass        string
}

var kinds = map[string]Kind{
	model.KindTransferSent: {
		Kind: model.KindTransferSent, Category: model.CategoryMoneyMovement,
		Priority: model.PriorityHigh, Requirement: model.RequirementTransactional,
		AuthoritativeEvent: events.TypeTransactionPosted, TemplateSchemaVersion: 1,
		DigestEligible: true,
		DefaultModes:   map[string]string{model.ChannelInApp: model.ModeImmediate, model.ChannelEmail: model.ModeImmediate, model.ChannelPush: model.ModeImmediate},
		DeepLinkPath:   "/transactions/{id}", PrivacyClass: "financial", RetentionClass: "financial_history",
	},
	model.KindTransferReceived: {
		Kind: model.KindTransferReceived, Category: model.CategoryMoneyMovement,
		Priority: model.PriorityHigh, Requirement: model.RequirementTransactional,
		AuthoritativeEvent: events.TypeTransactionPosted, TemplateSchemaVersion: 1,
		DigestEligible: true,
		DefaultModes:   map[string]string{model.ChannelInApp: model.ModeImmediate, model.ChannelEmail: model.ModeImmediate, model.ChannelPush: model.ModeImmediate},
		DeepLinkPath:   "/transactions/{id}", PrivacyClass: "financial", RetentionClass: "financial_history",
	},
	model.KindTopupSucceeded: {
		Kind: model.KindTopupSucceeded, Category: model.CategoryMoneyMovement,
		Priority: model.PriorityHigh, Requirement: model.RequirementTransactional,
		AuthoritativeEvent: events.TypeTransactionPosted, TemplateSchemaVersion: 1,
		DigestEligible: true,
		DefaultModes:   map[string]string{model.ChannelInApp: model.ModeImmediate, model.ChannelEmail: model.ModeImmediate, model.ChannelPush: model.ModeImmediate},
		DeepLinkPath:   "/topups/{id}", PrivacyClass: "financial", RetentionClass: "financial_history",
	},
	model.KindPayoutSucceeded: {
		Kind: model.KindPayoutSucceeded, Category: model.CategoryMoneyMovement,
		Priority: model.PriorityHigh, Requirement: model.RequirementTransactional,
		AuthoritativeEvent: events.TypeTransactionPosted, TemplateSchemaVersion: 1,
		DigestEligible: true,
		DefaultModes:   map[string]string{model.ChannelInApp: model.ModeImmediate, model.ChannelEmail: model.ModeImmediate, model.ChannelPush: model.ModeImmediate},
		DeepLinkPath:   "/payouts/{id}", PrivacyClass: "financial", RetentionClass: "financial_history",
	},
	model.KindPayoutCancelled: {
		Kind: model.KindPayoutCancelled, Category: model.CategoryMoneyMovement,
		Priority: model.PriorityHigh, Requirement: model.RequirementTransactional,
		AuthoritativeEvent: events.TypeTransactionPosted, TemplateSchemaVersion: 1,
		DigestEligible: true,
		DefaultModes:   map[string]string{model.ChannelInApp: model.ModeImmediate, model.ChannelEmail: model.ModeImmediate, model.ChannelPush: model.ModeImmediate},
		DeepLinkPath:   "/payouts/{id}", PrivacyClass: "financial", RetentionClass: "financial_history",
	},
	model.KindDailyDigest: {
		Kind: model.KindDailyDigest, Category: model.CategorySystem,
		Priority: model.PriorityNormal, Requirement: model.RequirementOptional,
		AuthoritativeEvent: "gateway.notification.digest.v1", TemplateSchemaVersion: 1,
		DefaultModes: map[string]string{model.ChannelEmail: model.ModeImmediate},
		PrivacyClass: "financial_summary", RetentionClass: "notification_delivery",
	},
}

func Lookup(kind string) (Kind, bool) {
	k, ok := kinds[kind]
	if !ok {
		return Kind{}, false
	}
	k.DefaultModes = cloneModes(k.DefaultModes)
	return k, true
}

func All() []Kind {
	out := make([]Kind, 0, len(kinds))
	for _, kind := range kinds {
		copy := kind
		copy.DefaultModes = cloneModes(kind.DefaultModes)
		out = append(out, copy)
	}
	return out
}

// KindForTransaction is the only current Ledger-to-notification mapping. The
// recipient role is explicit so a transfer cannot accidentally use one copy
// for both parties.
func KindForTransaction(transactionType, role string) (Kind, error) {
	var kind string
	switch transactionType {
	case "money_in":
		kind = model.KindTopupSucceeded
	case "transfer_p2p":
		switch role {
		case "sender":
			kind = model.KindTransferSent
		case "receiver":
			kind = model.KindTransferReceived
		default:
			return Kind{}, fmt.Errorf("notification: transfer recipient role %q is invalid", role)
		}
	case "withdraw_settle":
		kind = model.KindPayoutSucceeded
	case "withdraw_cancel":
		kind = model.KindPayoutCancelled
	default:
		return Kind{}, fmt.Errorf("notification: transaction type %q is not notifiable", transactionType)
	}
	value, ok := Lookup(kind)
	if !ok {
		return Kind{}, fmt.Errorf("notification: registry kind %q is missing", kind)
	}
	return value, nil
}

func cloneModes(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
