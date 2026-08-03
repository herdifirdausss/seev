// Package channel contains provider-neutral messages and results. Provider
// adapters accept already-rendered content; they never receive templates or
// raw domain events.
package channel

import "context"

type ProviderResult struct {
	Accepted          bool
	ProviderMessageID string
	StatusCode        int
	ErrorCode         string
	Permanent         bool
	InvalidEndpoint   bool
	ResponseExcerpt   string
}

type EmailMessage struct {
	DeliveryID     string
	NotificationID string
	To             string
	From           string
	ReplyTo        string
	Subject        string
	Text           string
	HTML           string
	MessageID      string
}

type EmailSender interface {
	Send(context.Context, EmailMessage) (ProviderResult, error)
}

type PushMessage struct {
	DeliveryID     string
	NotificationID string
	Token          string
	Platform       string
	Title          string
	Body           string
	Data           map[string]string
}

type PushSender interface {
	Send(context.Context, PushMessage) (ProviderResult, error)
}
