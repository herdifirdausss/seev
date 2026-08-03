package template

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/notify/model"
)

func testRenderContext() model.RenderContext {
	return model.RenderContext{
		NotificationID: uuid.NewString(),
		Amount:         model.MoneyContext{Minor: "125000", Currency: "IDR", Display: "IDR 125,000"},
		Transaction:    model.TransactionContext{ID: uuid.NewString(), PostedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		Action:         model.ActionContext{DeepLink: "/transactions/example?next=1&safe=2"},
	}
}

func TestRendererHTMLUsesContextEscaping(t *testing.T) {
	rendered, err := NewRenderer(0, 0).Render(Version{
		ID:               uuid.New(),
		Channel:          model.ChannelEmail,
		Status:           StatusActive,
		SubjectTemplate:  "Transaction update",
		TitleTemplate:    "Transaction",
		BodyTextTemplate: "Open the app",
		BodyHTMLTemplate: `<a href="{{.Action.DeepLink}}">Open Seev</a>`,
	}, testRenderContext())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.HTML, "&amp;") {
		t.Fatalf("HTML was not escaped: %s", rendered.HTML)
	}
}

func TestRendererRejectsMissingVariables(t *testing.T) {
	_, err := NewRenderer(0, 0).Render(Version{
		ID:               uuid.New(),
		Channel:          model.ChannelInApp,
		Status:           StatusActive,
		TitleTemplate:    "Update",
		BodyTextTemplate: "{{.UnknownField}}",
	}, testRenderContext())
	if err == nil {
		t.Fatal("expected missing template variable error")
	}
}

func TestRendererRejectsUnsafeSubjectAndHTMLOnInApp(t *testing.T) {
	context := testRenderContext()
	for _, tc := range []Version{
		{ID: uuid.New(), Channel: model.ChannelEmail, Status: StatusActive, SubjectTemplate: "bad\nsubject", BodyTextTemplate: "body"},
		{ID: uuid.New(), Channel: model.ChannelInApp, Status: StatusActive, TitleTemplate: "title", BodyTextTemplate: "body", BodyHTMLTemplate: "<p>body</p>"},
	} {
		if _, err := NewRenderer(0, 0).Render(tc, context); err == nil {
			t.Fatal("expected template safety error")
		}
	}
}
