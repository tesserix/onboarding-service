package auth

const (
	// The issuer Google stamps into its ID tokens, and the two endpoints for the
	// web-application flow.
	GoogleIssuer            = "https://accounts.google.com"
	GoogleAuthorizeEndpoint = "https://accounts.google.com/o/oauth2/v2/auth"
	GoogleTokenEndpoint     = "https://oauth2.googleapis.com/token"
	GoogleJWKSURL           = "https://www.googleapis.com/oauth2/v3/certs"
)

// NewGoogleClient builds the console sign-in client. Console humans authenticate
// against Google; Zitadel remains the authority for product machine tokens.
func NewGoogleClient(clientID, clientSecret, redirectURI string) *OIDCClient {
	return &OIDCClient{
		AuthorizeEndpoint: GoogleAuthorizeEndpoint,
		TokenEndpoint:     GoogleTokenEndpoint,
		ClientID:          clientID,
		ClientSecret:      clientSecret,
		RedirectURI:       redirectURI,
		Scopes:            []string{"openid", "email", "profile"},
	}
}
