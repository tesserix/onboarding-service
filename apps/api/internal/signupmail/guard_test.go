package signupmail

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// helivanta#1857: HTML comes only from layout.go and only resend.go talks to
// the provider, so a new email cannot ship a body or a transport of its own.
// The type system stops raw bytes reaching the transport; this scan stops the
// same-package escapes the compiler allows (building a framedMail or setting
// its payload outside layout.go).
func TestOnlyTheLayoutBuildsMailAndOnlyTheTransportNamesTheProvider(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		scanned++
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		layout, transport := path == "layout.go", path == "resend.go"
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BasicLit:
				if n.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(n.Value)
				if err != nil {
					t.Fatalf("%s: unreadable literal %s", path, n.Value)
				}
				lower := strings.ToLower(value)
				for _, marker := range []string{"<html", "<!doctype", "<table", "<body"} {
					if !layout && strings.Contains(lower, marker) {
						t.Fatalf("%s: string literal contains %q; email HTML must come only from the shared layout in layout.go (render it from a content value)", path, marker)
					}
				}
				if !layout && lower == "html" {
					t.Fatalf("%s: string literal %q is the provider's HTML key; only frame in layout.go builds the payload", path, value)
				}
				if !transport && strings.Contains(lower, "api.resend.com") {
					t.Fatalf("%s: names the email provider; only the transport in resend.go may send, and it accepts only a framedMail from layout.go", path)
				}
			case *ast.Ident:
				if n.Name == "framedMail" && !layout && !transport {
					t.Fatalf("%s: uses framedMail; senders get one only from c.frame in layout.go", path)
				}
			case *ast.CompositeLit:
				if ident, ok := n.Type.(*ast.Ident); ok && ident.Name == "framedMail" && !layout {
					t.Fatalf("%s: builds a framedMail; only frame in layout.go may", path)
				}
			case *ast.CallExpr:
				if ident, ok := n.Fun.(*ast.Ident); ok && ident.Name == "framedMail" && !layout {
					t.Fatalf("%s: converts to framedMail; only frame in layout.go may build one", path)
				}
			case *ast.SelectorExpr:
				if n.Sel.Name == "payload" && !layout && !transport {
					t.Fatalf("%s: touches a framedMail payload; only layout.go writes it and resend.go reads it", path)
				}
			}
			return true
		})
	}
	// The scan must have read the senders, or it proves nothing.
	if scanned < 6 {
		t.Fatalf("scanned %d source files; expected the layout, transport and every sender", scanned)
	}
}

// The framedMail refactor kept the wire payload byte-for-byte: frame produces
// exactly what senders marshalled before (render, then this map), and that is
// what reaches the provider.
func TestFramedPayloadMatchesThePreviousEnvelope(t *testing.T) {
	body := content{
		Subject:   "Your sign-in code — Helivanta",
		Preheader: "Enter this code to sign in. It expires in 10 minutes.",
		Heading:   "Your sign-in code",
		Line:      "Enter this code in the browser where you asked for it.",
		Code:      "12345678",
		Expiry:    "It expires in 10 minutes.",
		Note:      "Don't share this code with anyone.",
		Closing:   "Didn't ask for this code? You can ignore this email.",
	}
	html, text, err := render(body)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := json.Marshal(map[string]any{"from": "Helivanta <onboarding@helivanta.app>", "to": []string{"o@example.test"}, "subject": body.Subject, "html": html, "text": text})
	if err != nil {
		t.Fatal(err)
	}
	var wire []byte
	c := staffTestClient(t, transport(func(r *http.Request) (*http.Response, error) {
		wire, _ = io.ReadAll(r.Body)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"m"}`))}, nil
	}))
	c.from = "onboarding@helivanta.app"
	framed, err := c.frame("o@example.test", body)
	if err != nil || !bytes.Equal(framed.payload, previous) {
		t.Fatalf("frame changed the payload: %v\n%s\n%s", err, framed.payload, previous)
	}
	if _, err := c.SendDiscovery(t.Context(), "o@example.test", "12345678"); err != nil || !bytes.Equal(wire, previous) {
		t.Fatalf("sent payload differs from the previous envelope: %v\n%s", err, wire)
	}
}
