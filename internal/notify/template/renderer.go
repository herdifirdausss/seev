// Package template implements the restricted, deterministic notification
// renderer. It accepts only model.RenderContext, never an arbitrary event map.
package template

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	htmltemplate "html/template"
	"regexp"
	"strings"
	texttemplate "text/template"
	"time"

	"github.com/google/uuid"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/herdifirdausss/seev/internal/notify/model"
)

const (
	StatusDraft           = "draft"
	StatusPendingApproval = "pending_approval"
	StatusActive          = "active"
	StatusRetired         = "retired"
	StatusRejected        = "rejected"
	DefaultLocale         = "en-US"
	MaxRenderedTextBytes  = 32 * 1024
	MaxRenderedHTMLBytes  = 128 * 1024
)

var headerUnsafe = regexp.MustCompile(`[\r\n]`)
var minorPattern = regexp.MustCompile(`^-?[0-9]+$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
var titleCaser = cases.Title(language.English)

type Version struct {
	ID               uuid.UUID
	TemplateID       uuid.UUID
	Kind             string
	Channel          string
	Locale           string
	Version          int
	Status           string
	SubjectTemplate  string
	TitleTemplate    string
	BodyTextTemplate string
	BodyHTMLTemplate string
	CreatedBy        string
	SubmittedBy      string
	ApprovedBy       string
}

type Rendered struct {
	Subject string
	Title   string
	Text    string
	HTML    string
	Payload []byte
	Hash    []byte
}

type Renderer struct {
	MaxText int
	MaxHTML int
}

func NewRenderer(maxText, maxHTML int) *Renderer {
	if maxText <= 0 {
		maxText = MaxRenderedTextBytes
	}
	if maxHTML <= 0 {
		maxHTML = MaxRenderedHTMLBytes
	}
	return &Renderer{MaxText: maxText, MaxHTML: maxHTML}
}

func (r *Renderer) Render(version Version, context model.RenderContext) (Rendered, error) {
	if version.Status != "" && version.Status != StatusActive {
		return Rendered{}, fmt.Errorf("template version %s is not active", version.ID)
	}
	if err := ValidateRenderContext(context); err != nil {
		return Rendered{}, err
	}
	if version.Channel == model.ChannelInApp && version.BodyHTMLTemplate != "" {
		return Rendered{}, errors.New("in-app template cannot contain HTML")
	}
	if version.Channel == model.ChannelPush && version.BodyHTMLTemplate != "" {
		return Rendered{}, errors.New("push template cannot contain HTML")
	}
	if headerUnsafe.MatchString(version.SubjectTemplate) {
		return Rendered{}, errors.New("template subject contains a header control character")
	}

	textFuncs := texttemplate.FuncMap{
		"formatMoney":    FormatMoney,
		"formatDate":     func(value time.Time) string { return value.Format("2006-01-02") },
		"formatDateTime": func(value time.Time) string { return value.UTC().Format(time.RFC3339) },
		"upper":          strings.ToUpper, "lower": strings.ToLower, "title": titleCaser.String,
	}
	renderText := func(name, source string) (string, error) {
		if source == "" {
			return "", nil
		}
		t, err := texttemplate.New(name).Option("missingkey=error").Funcs(textFuncs).Parse(source)
		if err != nil {
			return "", fmt.Errorf("parse %s: %w", name, err)
		}
		var out bytes.Buffer
		if err := t.Execute(&out, context); err != nil {
			return "", fmt.Errorf("render %s: %w", name, err)
		}
		return normalize(out.String()), nil
	}

	subject, err := renderText("subject", version.SubjectTemplate)
	if err != nil {
		return Rendered{}, err
	}
	title, err := renderText("title", version.TitleTemplate)
	if err != nil {
		return Rendered{}, err
	}
	text, err := renderText("body_text", version.BodyTextTemplate)
	if err != nil {
		return Rendered{}, err
	}
	if headerUnsafe.MatchString(subject) {
		return Rendered{}, errors.New("rendered subject contains a header control character")
	}
	if len(text) > r.MaxText {
		return Rendered{}, fmt.Errorf("rendered text exceeds %d bytes", r.MaxText)
	}

	htmlBody := ""
	if version.BodyHTMLTemplate != "" {
		htmlFuncs := htmltemplate.FuncMap{
			"formatMoney":    FormatMoney,
			"formatDate":     func(value time.Time) string { return value.Format("2006-01-02") },
			"formatDateTime": func(value time.Time) string { return value.UTC().Format(time.RFC3339) },
			"upper":          strings.ToUpper, "lower": strings.ToLower, "title": titleCaser.String,
		}
		t, err := htmltemplate.New("body_html").Option("missingkey=error").Funcs(htmlFuncs).Parse(version.BodyHTMLTemplate)
		if err != nil {
			return Rendered{}, fmt.Errorf("parse body_html: %w", err)
		}
		var out bytes.Buffer
		if err := t.Execute(&out, context); err != nil {
			return Rendered{}, fmt.Errorf("render body_html: %w", err)
		}
		htmlBody = normalize(out.String())
		if len(htmlBody) > r.MaxHTML {
			return Rendered{}, fmt.Errorf("rendered html exceeds %d bytes", r.MaxHTML)
		}
	}
	payload, err := json.Marshal(struct {
		Subject string `json:"subject,omitempty"`
		Title   string `json:"title,omitempty"`
		Text    string `json:"text"`
		HTML    string `json:"html,omitempty"`
	}{subject, title, text, htmlBody})
	if err != nil {
		return Rendered{}, fmt.Errorf("marshal rendered payload: %w", err)
	}
	hash := sha256.Sum256(payload)
	return Rendered{Subject: subject, Title: title, Text: text, HTML: htmlBody, Payload: payload, Hash: hash[:]}, nil
}

// RenderDigest is the bounded renderer for the one supported aggregate
// notification. It uses a separate typed context so ordinary templates can
// never iterate arbitrary notification rows or event payloads.
func (r *Renderer) RenderDigest(version Version, context model.DigestRenderContext) (Rendered, error) {
	if version.Status != "" && version.Status != StatusActive {
		return Rendered{}, fmt.Errorf("template version %s is not active", version.ID)
	}
	if headerUnsafe.MatchString(version.SubjectTemplate) {
		return Rendered{}, errors.New("template subject contains a header control character")
	}
	if version.Channel != model.ChannelEmail {
		return Rendered{}, errors.New("digest template must target email")
	}
	textFuncs := texttemplate.FuncMap{
		"formatDate": func(value string) string { return value },
		"upper":      strings.ToUpper, "lower": strings.ToLower, "title": titleCaser.String,
	}
	renderText := func(name, source string) (string, error) {
		if source == "" {
			return "", nil
		}
		t, err := texttemplate.New(name).Option("missingkey=error").Funcs(textFuncs).Parse(source)
		if err != nil {
			return "", fmt.Errorf("parse %s: %w", name, err)
		}
		var out bytes.Buffer
		if err := t.Execute(&out, context); err != nil {
			return "", fmt.Errorf("render %s: %w", name, err)
		}
		return normalize(out.String()), nil
	}
	subject, err := renderText("subject", version.SubjectTemplate)
	if err != nil {
		return Rendered{}, err
	}
	title, err := renderText("title", version.TitleTemplate)
	if err != nil {
		return Rendered{}, err
	}
	textBody, err := renderText("body_text", version.BodyTextTemplate)
	if err != nil {
		return Rendered{}, err
	}
	if headerUnsafe.MatchString(subject) {
		return Rendered{}, errors.New("rendered subject contains a header control character")
	}
	if len(textBody) > r.MaxText {
		return Rendered{}, fmt.Errorf("rendered text exceeds %d bytes", r.MaxText)
	}
	htmlBody := ""
	if version.BodyHTMLTemplate != "" {
		t, err := htmltemplate.New("body_html").Option("missingkey=error").Funcs(htmltemplate.FuncMap{
			"formatDate": func(value string) string { return value },
			"upper":      strings.ToUpper, "lower": strings.ToLower, "title": titleCaser.String,
		}).Parse(version.BodyHTMLTemplate)
		if err != nil {
			return Rendered{}, fmt.Errorf("parse body_html: %w", err)
		}
		var out bytes.Buffer
		if err := t.Execute(&out, context); err != nil {
			return Rendered{}, fmt.Errorf("render body_html: %w", err)
		}
		htmlBody = normalize(out.String())
		if len(htmlBody) > r.MaxHTML {
			return Rendered{}, fmt.Errorf("rendered html exceeds %d bytes", r.MaxHTML)
		}
	}
	payload, err := json.Marshal(struct {
		Subject string `json:"subject,omitempty"`
		Title   string `json:"title,omitempty"`
		Text    string `json:"text"`
		HTML    string `json:"html,omitempty"`
	}{subject, title, textBody, htmlBody})
	if err != nil {
		return Rendered{}, fmt.Errorf("marshal rendered payload: %w", err)
	}
	hash := sha256.Sum256(payload)
	return Rendered{Subject: subject, Title: title, Text: textBody, HTML: htmlBody, Payload: payload, Hash: hash[:]}, nil
}

func Builtin(kind, channel, locale string) (Version, bool) {
	if locale == "" {
		locale = DefaultLocale
	}
	id := uuid.NewSHA1(uuid.Nil, []byte(kind+":"+channel+":"+locale+":v1"))
	v := Version{ID: id, Kind: kind, Channel: channel, Locale: locale, Version: 1, Status: StatusActive}
	if kind == model.KindDailyDigest {
		if channel != model.ChannelEmail {
			return Version{}, false
		}
		v.SubjectTemplate = "Your Seev daily notification digest"
		v.TitleTemplate = "Your Seev daily notification digest"
		v.BodyTextTemplate = "{{range .Items}}- {{.Title}}: {{.Body}}\n{{end}}{{if .MoreCount}}\nAnd {{.MoreCount}} more notification(s).\n{{end}}"
		v.BodyHTMLTemplate = "<h1>Your Seev daily notification digest</h1><ul>{{range .Items}}<li><strong>{{.Title}}</strong>: {{.Body}}</li>{{end}}</ul>{{if .MoreCount}}<p>And {{.MoreCount}} more notification(s).</p>{{end}}"
		return v, true
	}
	amount := "{{.Amount.Display}}"
	switch kind {
	case model.KindTransferSent:
		v.TitleTemplate, v.BodyTextTemplate, v.SubjectTemplate = "Transfer sent", "Your "+amount+" transfer was sent successfully.", "Your transfer was sent"
	case model.KindTransferReceived:
		v.TitleTemplate, v.BodyTextTemplate, v.SubjectTemplate = "Transfer received", "You received "+amount+".", "You received a transfer"
	case model.KindTopupSucceeded:
		v.TitleTemplate, v.BodyTextTemplate, v.SubjectTemplate = "Funds received", "Your "+amount+" top-up was successful and your balance increased.", "Your top-up was successful"
	case model.KindPayoutSucceeded:
		v.TitleTemplate, v.BodyTextTemplate, v.SubjectTemplate = "Withdrawal successful", "Your "+amount+" withdrawal was processed successfully.", "Your withdrawal was successful"
	case model.KindPayoutCancelled:
		v.TitleTemplate, v.BodyTextTemplate, v.SubjectTemplate = "Withdrawal canceled", "Your "+amount+" withdrawal was canceled and the funds were returned.", "Your withdrawal was canceled"
	default:
		return Version{}, false
	}
	if locale == "id-ID" {
		switch kind {
		case model.KindTransferSent:
			v.TitleTemplate, v.BodyTextTemplate, v.SubjectTemplate = "Transfer terkirim", "Transfer "+amount+" Anda berhasil dikirim.", "Transfer Anda berhasil dikirim"
		case model.KindTransferReceived:
			v.TitleTemplate, v.BodyTextTemplate, v.SubjectTemplate = "Transfer diterima", "Anda menerima "+amount+".", "Anda menerima transfer"
		case model.KindTopupSucceeded:
			v.TitleTemplate, v.BodyTextTemplate, v.SubjectTemplate = "Dana diterima", "Isi ulang "+amount+" Anda berhasil dan saldo bertambah.", "Isi ulang Anda berhasil"
		case model.KindPayoutSucceeded:
			v.TitleTemplate, v.BodyTextTemplate, v.SubjectTemplate = "Penarikan berhasil", "Penarikan "+amount+" Anda berhasil diproses.", "Penarikan Anda berhasil"
		case model.KindPayoutCancelled:
			v.TitleTemplate, v.BodyTextTemplate, v.SubjectTemplate = "Penarikan dibatalkan", "Penarikan "+amount+" Anda dibatalkan dan dana dikembalikan.", "Penarikan Anda dibatalkan"
		}
	}
	switch channel {
	case model.ChannelInApp:
		return v, true
	case model.ChannelEmail:
		v.BodyHTMLTemplate = "<p>" + html.EscapeString(v.BodyTextTemplate) + "</p>"
		return v, true
	case model.ChannelPush:
		v.TitleTemplate = "Transaction update"
		v.BodyTextTemplate = "Open Seev to view the details."
		if locale == "id-ID" {
			v.TitleTemplate = "Pembaruan transaksi"
			v.BodyTextTemplate = "Buka Seev untuk melihat detailnya."
		}
		v.SubjectTemplate = ""
		v.BodyHTMLTemplate = ""
		return v, true
	default:
		return Version{}, false
	}
}

// ValidateRenderContext rejects malformed money and links before a template
// can turn them into user-facing prose. The source event validator already
// enforces the same shape, but this second boundary also protects stored
// context and operator preview fixtures.
func ValidateRenderContext(value model.RenderContext) error {
	if value.NotificationID == "" || value.Transaction.ID == "" {
		return errors.New("render context is missing an identifier")
	}
	if !minorPattern.MatchString(value.Amount.Minor) {
		return errors.New("render context amount is not an integer minor-unit value")
	}
	if !currencyPattern.MatchString(value.Amount.Currency) {
		return errors.New("render context currency is not an ISO-4217 code")
	}
	if value.Action.DeepLink == "" || headerUnsafe.MatchString(value.Action.DeepLink) || !strings.HasPrefix(value.Action.DeepLink, "/") {
		return errors.New("render context deep link is invalid")
	}
	expected := formatMoney(value.Amount.Minor, value.Amount.Currency)
	if value.Amount.Display != "" && value.Amount.Display != expected {
		return errors.New("render context money display is not canonical")
	}
	return nil
}

func FormatMoney(value model.MoneyContext) string {
	if value.Minor == "" || value.Currency == "" || !minorPattern.MatchString(value.Minor) || !currencyPattern.MatchString(value.Currency) {
		return ""
	}
	return formatMoney(value.Minor, value.Currency)
}

func formatMoney(minor, currency string) string {
	negative := strings.HasPrefix(minor, "-")
	if negative {
		minor = strings.TrimPrefix(minor, "-")
	}
	decimals := 2
	switch currency {
	case "IDR", "JPY", "KRW":
		decimals = 0
	}
	for len(minor) <= decimals {
		minor = "0" + minor
	}
	whole, fraction := minor, ""
	if decimals > 0 {
		whole, fraction = minor[:len(minor)-decimals], minor[len(minor)-decimals:]
	}
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	result := currency + " " + whole
	if decimals > 0 {
		result += "." + fraction
	}
	if negative {
		result = "-" + result
	}
	return result
}

func normalize(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}
