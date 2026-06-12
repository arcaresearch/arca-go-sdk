package arca

import (
	"context"
	"net/http"
)

// DefaultBaseURL is the production Arca API base URL.
const DefaultBaseURL = "https://api.arcaos.io"

// DefaultCandleCDNBaseURL is the production candle-data CDN base URL.
const DefaultCandleCDNBaseURL = "https://data.arcaos.io"

// TokenProvider returns a fresh scoped JWT. It is called on first use, before
// expiry, and on HTTP 401. Concurrent calls are deduplicated by the SDK.
type TokenProvider func(ctx context.Context) (string, error)

// Config configures an Arca client. Provide either APIKey+Realm (backend) or
// Token / TokenProvider (frontend / end-user). For token configs, Realm may be
// omitted to use the realm embedded in the JWT.
type Config struct {
	// APIKey authenticates as the builder (server-side). Format: arca_<prefix>_<secret>.
	APIKey string
	// Token is a scoped JWT. Optional when TokenProvider is set.
	Token string
	// TokenProvider supplies fresh scoped JWTs.
	TokenProvider TokenProvider
	// Realm is the realm slug, UUID, or TypeID. Required for API-key configs.
	Realm string

	// BaseURL overrides the API base URL. Defaults to DefaultBaseURL.
	BaseURL string
	// CandleCDNBaseURL overrides the candle CDN. Nil uses the default; a
	// pointer to "" disables the CDN (REST only).
	CandleCDNBaseURL *string

	// HTTPClient overrides the underlying *http.Client.
	HTTPClient *http.Client

	// StepUpHandler intercepts HTTP 412 STEP_UP_REQUIRED responses.
	StepUpHandler StepUpHandler

	// Cache configures the in-memory history cache. Nil uses defaults
	// (50 entries, 5-minute TTL). Set Disabled to turn caching off.
	Cache *CacheConfig
}

// Fees holds the platform's fixed fees. The Arca network takes no transfer
// fee — only applications (builder / application fees) and the underlying
// venue charge.
var Fees = struct {
	ExchangeTransfer string
}{
	ExchangeTransfer: "0",
}

// New creates an Arca client from a Config. Call Ready before other methods to
// resolve the realm slug to an internal id (or do it lazily — every method
// resolves the realm on first use).
func New(cfg Config) (*Arca, error) {
	if cfg.APIKey == "" && cfg.Token == "" && cfg.TokenProvider == nil {
		return nil, &ArcaError{Code: "INVALID_CONFIG", Message: "Config must include either APIKey, Token, or TokenProvider"}
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	baseURL = trimTrailingSlash(baseURL)
	apiBase := baseURL + "/api/v1"

	a := &Arca{
		realmInput: cfg.Realm,
		cache:      newHistoryCache(cfg.Cache),
	}

	cdn := DefaultCandleCDNBaseURL
	if cfg.CandleCDNBaseURL != nil {
		cdn = *cfg.CandleCDNBaseURL
	}
	if cdn != "" {
		a.candleCDNBaseURL = trimTrailingSlash(cdn)
	}

	if cfg.APIKey != "" {
		a.client = newHTTPClient(clientConfig{
			credential:    cfg.APIKey,
			credType:      credAPIKey,
			baseURL:       apiBase,
			httpClient:    cfg.HTTPClient,
			stepUpHandler: cfg.StepUpHandler,
		})
		a.credType = credAPIKey
		a.apiKey = cfg.APIKey
	} else {
		a.tokenProvider = cfg.TokenProvider
		var onUnauthorized func(ctx context.Context, trigger AuthRefreshTrigger) (string, error)
		if a.tokenProvider != nil {
			onUnauthorized = func(ctx context.Context, trigger AuthRefreshTrigger) (string, error) {
				tok, err := a.refreshTokenFromProvider(ctx)
				if err == nil && trigger == AuthRefreshForbidden {
					// The cached token was valid but its scope no longer
					// matched the request (e.g. the app switched signed-in
					// users). A live WebSocket session is still authenticated
					// under the old identity — force it to re-auth and
					// re-subscribe with the fresh token.
					a.ws.Reconnect()
				}
				return tok, err
			}
		}
		a.client = newHTTPClient(clientConfig{
			credential:     cfg.Token,
			credType:       credToken,
			baseURL:        apiBase,
			httpClient:     cfg.HTTPClient,
			onUnauthorized: onUnauthorized,
			onAuthError:    a.emitAuthError,
			stepUpHandler:  cfg.StepUpHandler,
		})
		a.credType = credToken
		if cfg.Token != "" {
			a.extractRealmFromToken(cfg.Token)
		}
	}

	var getToken func(ctx context.Context) (string, error)
	if a.tokenProvider != nil {
		getToken = a.refreshTokenFromProvider
	}
	a.ws = newWebSocketManager(wsConfig{
		baseURL:    apiBase,
		credential: firstNonEmpty(cfg.APIKey, cfg.Token),
		credType:   a.credType,
		getRealmID: func() string { return a.currentRealmID() },
		getToken:   getToken,
	})

	return a, nil
}

// FromToken creates a client from a scoped JWT. The realm is extracted from the
// token claims when present.
func FromToken(token string, cfg Config) (*Arca, error) {
	cfg.Token = token
	cfg.APIKey = ""
	return New(cfg)
}

// FromTokenProvider creates a client with automatic token management.
func FromTokenProvider(provider TokenProvider, cfg Config) (*Arca, error) {
	cfg.TokenProvider = provider
	cfg.APIKey = ""
	return New(cfg)
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
