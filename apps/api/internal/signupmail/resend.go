// Package signupmail sends native identity links through Helivanta's email provider.
package signupmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

var ErrUnavailable = errors.New("email delivery unavailable")
var sourceID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

type Client struct {
	keyFile string
	from    string
	http    *http.Client
}

// Message is transient; native codes must never be persisted or logged.
type Message struct{ Email, Name, Organization, UserID, Code, Phase string }

func (Message) String() string   { return "[redacted verification email]" }
func (Message) GoString() string { return "[redacted verification email]" }

// New restricts sending to the product domains and reads the key from a runtime file.
func New(keyFile, from string, transport http.RoundTripper) (*Client, error) {
	if keyFile == "" || (from != "onboarding@helivanta.app" && from != "onboarding@helivanta.com") {
		return nil, ErrUnavailable
	}
	return &Client{keyFile: keyFile, from: from, http: &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

var emailTemplate = template.Must(template.New("verification").Parse(`<!doctype html><html><body><h1>{{.Title}}</h1><p>Hello {{.Name}},</p><p>Continue setting up {{.Organization}} on Helivanta.</p><p><a href="{{.Link}}">{{.Title}}</a></p><p>This link expires after 1 hour. If it has expired, request a fresh link from workspace setup. Up to 3 emails per hour and 10 per 24 hours, per workspace, including initial delivery and password setup. Across workspaces, the same recipient is limited to 10 emails per hour and 30 per 24 hours. Once your email is verified, verification emails stop.</p><p>If you did not request this, you can ignore this email.</p></body></html>`))

// Send returns provider acceptance evidence, not an assertion of inbox delivery.
func (c *Client) Send(ctx context.Context, message Message) (string, error) {
	address, err := mail.ParseAddress(message.Email)
	if err != nil || address.Name != "" || address.Address != message.Email || len(message.Email) > 200 || !sourceID.MatchString(message.UserID) || message.Code == "" || len(message.Code) > 20 || strings.ContainsFunc(message.Code, unicode.IsControl) || (message.Phase != "email" && message.Phase != "password") {
		return "", ErrUnavailable
	}
	title := "Verify your email address"
	if message.Phase == "password" {
		title = "Set your password"
	}
	link := url.URL{Scheme: "https", Host: "helivanta.app", Path: "/signup/verify", Fragment: url.Values{"phase": {message.Phase}, "userId": {message.UserID}, "code": {message.Code}}.Encode()}
	var html bytes.Buffer
	if emailTemplate.Execute(&html, struct{ Title, Name, Organization, Link string }{title, message.Name, message.Organization, link.String()}) != nil {
		return "", ErrUnavailable
	}
	payload, err := json.Marshal(map[string]any{"from": "Helivanta <" + c.from + ">", "to": []string{message.Email}, "subject": title + " — Helivanta", "html": html.String(), "text": "Hello " + message.Name + ",\n\nContinue setting up " + message.Organization + " on Helivanta.\n\n" + link.String() + "\n\nThis link expires after 1 hour. If it has expired, request a fresh link from https://helivanta.app/signup. Up to 3 emails per hour and 10 per 24 hours, per workspace, including initial delivery and password setup. Across workspaces, the same recipient is limited to 10 emails per hour and 30 per 24 hours. Once your email is verified, verification emails stop.\n\nIf you did not request this, you can ignore this email."})
	if err != nil {
		return "", ErrUnavailable
	}
	return c.sendPayload(ctx, payload)
}

func (c *Client) sendPayload(ctx context.Context, payload []byte) (string, error) {
	return c.sendPayloadWithKey(ctx, payload, uuid.NewString())
}

func (c *Client) sendPayloadWithKey(ctx context.Context, payload []byte, idempotencyKey string) (string, error) {
	file, err := os.Open(c.keyFile)
	if err != nil {
		return "", ErrUnavailable
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 4097))
	closeErr := file.Close()
	key := strings.TrimSpace(string(raw))
	if readErr != nil || closeErr != nil || len(raw) > 4096 || !strings.HasPrefix(key, "re_") || strings.ContainsFunc(key, unicode.IsSpace) || strings.ContainsFunc(key, unicode.IsControl) {
		return "", ErrUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(payload))
	if err != nil {
		return "", ErrUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	response, err := c.http.Do(request)
	if err != nil {
		return "", ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil || len(body) > 8192 || response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", ErrUnavailable
	}
	var accepted struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &accepted) != nil || !sourceID.MatchString(accepted.ID) {
		return "", ErrUnavailable
	}
	return accepted.ID, nil
}
