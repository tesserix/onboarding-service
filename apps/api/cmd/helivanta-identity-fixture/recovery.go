//go:build identityfixture

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/signupmail"
)

var errFixtureMail = errors.New("isolated recovery mailbox unavailable")
var fixtureCode = regexp.MustCompile(`^Your Helivanta password recovery code is ([0-9]{8})\.\n\n`)

func fixtureHandler(next http.Handler, recovery bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read := r.Method == http.MethodPost && slices.Contains([]string{
			"/internal/v1/customer-signups/identity-projection",
			"/internal/v1/customer-signups/login-target",
		}, r.URL.Path)
		credential := recovery && r.Method == http.MethodPost && slices.Contains([]string{
			"/internal/v1/customer-signups/password-recovery/start",
			"/internal/v1/customer-signups/password-recovery/verify",
			"/internal/v1/customer-signups/password-recovery/status",
			"/internal/v1/customer-signups/password-recovery/prepare",
			"/internal/v1/customer-signups/password-recovery/cancel",
			"/internal/v1/customer-signups/password-recovery/password",
		}, r.URL.Path)
		if read || credential || r.Method == http.MethodGet && r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

func newRecoveryMailbox(dir string) (*signupmail.Client, func(), error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, nil, errFixtureMail
	}
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, nil, errFixtureMail
	}
	parent, parentErr := filepath.EvalSymlinks(filepath.Dir(dir))
	resolved, err := filepath.EvalSymlinks(dir)
	info, statErr := os.Lstat(dir)
	if parentErr != nil || err != nil || resolved != filepath.Join(parent, filepath.Base(dir)) || statErr != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, nil, errFixtureMail
	}
	key, err := os.CreateTemp(dir, ".fixture-mail-key-")
	if err != nil {
		return nil, nil, errFixtureMail
	}
	cleanup := func() { _ = os.Remove(key.Name()) }
	// This dummy is consumed only by the in-process, network-free test transport.
	if _, err = key.WriteString("re_helivanta_fixture_not_a_provider_key"); err != nil {
		_ = key.Close()
		cleanup()
		return nil, nil, errFixtureMail
	}
	if err = key.Close(); err != nil {
		cleanup()
		return nil, nil, errFixtureMail
	}
	client, err := signupmail.New(key.Name(), "onboarding@helivanta.com", recoveryMailCapture{dir: resolved})
	if err != nil {
		cleanup()
		return nil, nil, errFixtureMail
	}
	return client, cleanup, nil
}

type recoveryMailCapture struct{ dir string }

func (capture recoveryMailCapture) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body == nil {
		return nil, errFixtureMail
	}
	defer func() { _ = r.Body.Close() }()
	if r.Method != http.MethodPost || r.URL.String() != "https://api.resend.com/emails" || r.Header.Get("Authorization") != "Bearer re_helivanta_fixture_not_a_provider_key" {
		return nil, errFixtureMail
	}
	idText := strings.TrimPrefix(r.Header.Get("Idempotency-Key"), "helivanta-password-recovery-")
	id, err := uuid.Parse(idText)
	if err != nil || id.Version() != 7 || id.Variant() != uuid.RFC4122 || id.String() != idText || r.Header.Get("Idempotency-Key") != "helivanta-password-recovery-"+idText {
		return nil, errFixtureMail
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	if err != nil || len(raw) > 8192 {
		return nil, errFixtureMail
	}
	var message struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
		HTML    string   `json:"html"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&message) != nil || decoder.Decode(new(any)) != io.EOF || message.From != "Helivanta <onboarding@helivanta.com>" || message.Subject != "Recover your Helivanta password" || len(message.To) != 1 || !slices.Contains([]string{"e2e-password-recovery-native-admin@helivanta.dev", "e2e-password-recovery-native-pharmacist@helivanta.dev"}, message.To[0]) {
		return nil, errFixtureMail
	}
	match := fixtureCode.FindStringSubmatch(message.Text)
	if len(match) != 2 {
		return nil, errFixtureMail
	}
	proof, _ := json.Marshal(struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}{message.To[0], match[1]})
	path := filepath.Join(capture.dir, idText+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 512 {
			return nil, errFixtureMail
		}
		previous, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(previous, proof) {
			return nil, errFixtureMail
		}
	} else if err != nil {
		return nil, errFixtureMail
	} else {
		_, writeErr := file.Write(proof)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return nil, errFixtureMail
		}
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"fixture-` + idText + `"}`))}, nil
}
