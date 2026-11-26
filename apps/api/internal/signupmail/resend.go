package signupmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// Send returns provider acceptance evidence, not an assertion of inbox delivery.
func (c *Client) Send(ctx context.Context, message Message) (string, error) {
	address, err := mail.ParseAddress(message.Email)
	if err != nil || address.Name != "" || address.Address != message.Email || len(message.Email) > 200 || !sourceID.MatchString(message.UserID) || message.Code == "" || len(message.Code) > 20 || strings.ContainsFunc(message.Code, unicode.IsControl) || (message.Phase != "email" && message.Phase != "password") {
		return "", ErrUnavailable
	}
	link := url.URL{Scheme: "https", Host: "helivanta.app", Path: "/signup/verify", Fragment: url.Values{"phase": {message.Phase}, "userId": {message.UserID}, "code": {message.Code}}.Encode()}
	// The email link opens the password form (helivanta#1763), so both phases
	// say what the person will do next, not which native step this is.
	body := content{
		Subject:   "Verify your email for " + message.Organization + " — Helivanta",
		Preheader: "Confirm your email to finish setting up " + message.Organization + ".",
		Heading:   "Verify your email",
		Greeting:  greeting(message.Name),
		Line:      "Confirm this is your email and set your password to finish setting up " + message.Organization + " on Helivanta.",
		Button:    "Continue setup",
		URL:       link.String(),
		Expiry:    "This link expires in 1 hour.",
	}
	if message.Phase == "password" {
		body.Subject = "Set your password — Helivanta"
		body.Preheader = "Set a password to finish setting up " + message.Organization + "."
		body.Heading = "Set your password"
		body.Line = "Set a password for " + message.Organization + " on Helivanta."
		body.Button = "Set password"
	}
	framed, err := c.frame(message.Email, body)
	if err != nil {
		return "", err
	}
	return c.sendPayload(ctx, framed)
}

func (c *Client) sendPayload(ctx context.Context, mail framedMail) (string, error) {
	return c.sendPayloadWithKey(ctx, mail, uuid.NewString())
}

// sendPayloadWithKey takes a framedMail, not bytes: only layout.go can make one.
func (c *Client) sendPayloadWithKey(ctx context.Context, mail framedMail, idempotencyKey string) (string, error) {
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
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(mail.payload))
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
