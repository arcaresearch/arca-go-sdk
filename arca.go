package arca

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const refreshBuffer = 30 * time.Second

var (
	uuidRe   = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	typeidRe = regexp.MustCompile(`(?i)^[a-z]{2,63}_[0-9a-hjkmnp-tv-z]{26}$`)
)

func isIDFormat(v string) bool { return uuidRe.MatchString(v) || typeidRe.MatchString(v) }

// Arca is the SDK client. Create one with New, FromToken, or FromTokenProvider.
// Call Ready before issuing requests (or rely on lazy resolution — every method
// resolves the realm on first use).
type Arca struct {
	client *httpClient
	ws     *WebSocketManager

	credType credentialType
	apiKey   string

	realmInput      string
	realmMu         sync.Mutex
	resolvedRealmID string
	readyMu         sync.Mutex

	tokenProvider TokenProvider
	refreshMu     sync.Mutex
	refreshDone   chan struct{}
	refreshResult refreshResult
	refreshTimer  *time.Timer

	authErrMu        sync.Mutex
	authErrListeners map[int]func(error)
	nextAuthErrID    int

	cache            *historyCache
	candleCDNBaseURL string

	metaMu    sync.Mutex
	metaCache map[string]SimMetaAsset

	twapMu     sync.Mutex
	twapLimits *TwapLimits
}

type refreshResult struct {
	token string
	err   error
}

// WS returns the WebSocket manager for real-time events.
func (a *Arca) WS() *WebSocketManager { return a.ws }

// Ready resolves the realm slug to an internal id. It is called automatically
// on first use but can be called explicitly for eager initialization.
func (a *Arca) Ready(ctx context.Context) error { return a.ensureReady(ctx) }

func (a *Arca) ensureReady(ctx context.Context) error {
	if a.getResolvedRealmID() != "" {
		return nil
	}
	a.readyMu.Lock()
	defer a.readyMu.Unlock()
	if a.getResolvedRealmID() != "" {
		return nil
	}
	if a.tokenProvider != nil && a.client.getCredential() == "" {
		tok, err := a.refreshTokenFromProvider(ctx)
		if err != nil {
			return err
		}
		a.extractRealmFromToken(tok)
		if a.getResolvedRealmID() != "" {
			return nil
		}
	}
	return a.resolveRealm(ctx)
}

func (a *Arca) getResolvedRealmID() string {
	a.realmMu.Lock()
	defer a.realmMu.Unlock()
	return a.resolvedRealmID
}

func (a *Arca) setRealmID(id string) {
	a.realmMu.Lock()
	a.resolvedRealmID = id
	a.realmMu.Unlock()
}

func (a *Arca) currentRealmID() string { return a.getResolvedRealmID() }

// realmID returns the resolved realm id, resolving lazily. Used by methods that
// must include realmId in the request.
func (a *Arca) realmID(ctx context.Context) (string, error) {
	if err := a.ensureReady(ctx); err != nil {
		return "", err
	}
	return a.getResolvedRealmID(), nil
}

func (a *Arca) resolveRealm(ctx context.Context) error {
	if a.getResolvedRealmID() != "" {
		return nil
	}
	if a.realmInput == "" {
		return &ArcaError{Code: "NO_REALM", Message: "No realm specified. Provide Realm in the config or use a scoped token that contains a realmId claim."}
	}
	if isIDFormat(a.realmInput) {
		a.setRealmID(a.realmInput)
		return nil
	}
	var resp RealmListResponse
	if err := a.client.get(ctx, "/realms", nil, &resp); err != nil {
		return err
	}
	for _, r := range resp.Realms {
		if r.Slug == a.realmInput || r.Name == a.realmInput || r.ID == a.realmInput {
			a.setRealmID(r.ID)
			return nil
		}
	}
	slugs := make([]string, 0, len(resp.Realms))
	for _, r := range resp.Realms {
		slugs = append(slugs, r.Slug)
	}
	return &NotFoundError{newArcaError("REALM_NOT_FOUND", "Realm '"+a.realmInput+"' not found. Available realms: "+strings.Join(slugs, ", "), "")}
}

// UpdateToken replaces the scoped JWT used for HTTP and WebSocket auth.
func (a *Arca) UpdateToken(token string) {
	a.client.updateCredential(token)
	a.ws.updateToken(token)
	a.scheduleProactiveRefresh(token)
}

// SetStepUpHandler registers (or clears, with nil) the handler invoked on a
// 412 STEP_UP_REQUIRED response.
func (a *Arca) SetStepUpHandler(h StepUpHandler) { a.client.setStepUpHandler(h) }

// OnAuthError registers a listener for unrecoverable auth errors and returns an
// unsubscribe func.
func (a *Arca) OnAuthError(listener func(error)) func() {
	a.authErrMu.Lock()
	if a.authErrListeners == nil {
		a.authErrListeners = map[int]func(error){}
	}
	id := a.nextAuthErrID
	a.nextAuthErrID++
	a.authErrListeners[id] = listener
	a.authErrMu.Unlock()
	return func() {
		a.authErrMu.Lock()
		delete(a.authErrListeners, id)
		a.authErrMu.Unlock()
	}
}

func (a *Arca) emitAuthError(err error) {
	a.authErrMu.Lock()
	cbs := make([]func(error), 0, len(a.authErrListeners))
	for _, cb := range a.authErrListeners {
		cbs = append(cbs, cb)
	}
	a.authErrMu.Unlock()
	for _, cb := range cbs {
		cb(err)
	}
}

// ClearHistoryCache clears the in-memory cache of historical-data responses.
func (a *Arca) ClearHistoryCache() { a.cache.clear() }

// Dispose stops background timers and disconnects the WebSocket.
func (a *Arca) Dispose() {
	a.refreshMu.Lock()
	if a.refreshTimer != nil {
		a.refreshTimer.Stop()
		a.refreshTimer = nil
	}
	a.refreshMu.Unlock()
	a.ws.Disconnect()
}

// ---- Token lifecycle ----

func (a *Arca) extractRealmFromToken(token string) {
	if a.realmInput == "" && a.getResolvedRealmID() == "" {
		if claims := decodeJWTPayload(token); claims != nil {
			if rid, ok := claims["realmId"].(string); ok && rid != "" {
				a.setRealmID(rid)
			}
		}
	}
	a.scheduleProactiveRefresh(token)
}

func (a *Arca) refreshTokenFromProvider(ctx context.Context) (string, error) {
	a.refreshMu.Lock()
	if a.refreshDone != nil {
		ch := a.refreshDone
		a.refreshMu.Unlock()
		<-ch
		a.refreshMu.Lock()
		r := a.refreshResult
		a.refreshMu.Unlock()
		return r.token, r.err
	}
	ch := make(chan struct{})
	a.refreshDone = ch
	a.refreshMu.Unlock()

	tok, err := a.tokenProvider(ctx)

	a.refreshMu.Lock()
	a.refreshResult = refreshResult{token: tok, err: err}
	a.refreshDone = nil
	close(ch)
	a.refreshMu.Unlock()

	if err == nil {
		a.applyNewToken(tok)
	}
	return tok, err
}

func (a *Arca) applyNewToken(token string) {
	a.client.updateCredential(token)
	a.ws.updateToken(token)
	a.scheduleProactiveRefresh(token)
}

func (a *Arca) scheduleProactiveRefresh(token string) {
	if a.tokenProvider == nil {
		return
	}
	claims := decodeJWTPayload(token)
	if claims == nil {
		return
	}
	expF, ok := claims["exp"].(float64)
	if !ok {
		return
	}
	expiresAt := time.Unix(int64(expF), 0)
	delay := time.Until(expiresAt) - refreshBuffer
	if delay < 0 {
		delay = 0
	}
	a.refreshMu.Lock()
	if a.refreshTimer != nil {
		a.refreshTimer.Stop()
	}
	a.refreshTimer = time.AfterFunc(delay, func() {
		if _, err := a.refreshTokenFromProvider(context.Background()); err != nil {
			a.emitAuthError(err)
		}
	})
	a.refreshMu.Unlock()
}

func decodeJWTPayload(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// tolerate standard padding variants
		raw, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil
		}
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	return claims
}

// ---- Operation helpers ----

// op wraps a mutation HTTP call into an OperationHandle. It defers ready() and
// failure-checking into the background submission.
func op[T any](
	a *Arca,
	ctx context.Context,
	call func() (T, error),
	opOf func(T) *Operation,
	setOp func(*T, *Operation),
	predicted *PredictedEffect,
	defaultTimeout time.Duration,
) *OperationHandle[T] {
	return newOperationHandle(
		func() (T, error) {
			var zero T
			if err := a.ensureReady(ctx); err != nil {
				return zero, err
			}
			resp, err := call()
			if err != nil {
				return resp, err
			}
			if o := opOf(resp); o != nil && (o.State == OpFailed || o.State == OpExpired) {
				return resp, newOperationFailedError(o.snapshot())
			}
			return resp, nil
		},
		opOf, setOp,
		func(c context.Context, id string, t time.Duration) (*Operation, error) {
			return a.waitForOperation(c, id, t)
		},
		predicted, defaultTimeout,
	)
}

// WaitForOperation blocks until the operation reaches a terminal state. A
// timeout of 0 uses the 30s default. Returns *OperationFailedError on failure
// and *OperationStalledError on timeout.
func (a *Arca) WaitForOperation(ctx context.Context, operationID string, timeout time.Duration) (*Operation, error) {
	return a.waitForOperation(ctx, operationID, timeout)
}

func (a *Arca) waitForOperation(ctx context.Context, operationID string, timeout time.Duration) (*Operation, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	a.ws.EnsureConnected()
	go func() { _, _ = a.ws.watchPath(context.Background(), "/") }()
	defer a.ws.unwatchPath("/")

	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resultCh := make(chan *Operation, 1)
	errCh := make(chan error, 1)
	var settled atomic.Bool
	var lastMu sync.Mutex
	var lastOp *Operation

	tryResolve := func(o *Operation) {
		if o == nil {
			return
		}
		lastMu.Lock()
		lastOp = o
		lastMu.Unlock()
		if o.State == OpPending {
			return
		}
		if settled.CompareAndSwap(false, true) {
			if o.State == OpFailed || o.State == OpExpired {
				errCh <- newOperationFailedError(o.snapshot())
			} else {
				resultCh <- o
			}
		}
	}
	fetchAndResolve := func() {
		var detail OperationDetailResponse
		if err := a.client.get(deadlineCtx, "/operations/"+operationID, nil, &detail); err == nil {
			tryResolve(&detail.Operation)
		}
	}

	unsub := a.ws.OnOperationUpdated(func(o *Operation, ev RealmEvent) {
		if ev.EntityID != operationID {
			return
		}
		if o != nil {
			tryResolve(o)
		} else {
			go fetchAndResolve()
		}
	})
	defer unsub()

	go fetchAndResolve()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadlineCtx.Done():
			timeoutMS := timeout.Milliseconds()
			var detail OperationDetailResponse
			fetchCtx, fc := context.WithTimeout(context.Background(), 5*time.Second)
			err := a.client.get(fetchCtx, "/operations/"+operationID, nil, &detail)
			fc()
			if err == nil {
				snap := detail.Operation.snapshot()
				return nil, newOperationStalledError(operationID, timeoutMS, &snap)
			}
			lastMu.Lock()
			lo := lastOp
			lastMu.Unlock()
			var sp *OperationSnapshot
			if lo != nil {
				s := lo.snapshot()
				sp = &s
			}
			return nil, newOperationStalledError(operationID, timeoutMS, sp)
		case o := <-resultCh:
			return o, nil
		case err := <-errCh:
			return nil, err
		case <-ticker.C:
			go fetchAndResolve()
		}
	}
}

// validatePath enforces the leading-slash convention.
func validatePath(path string) error {
	if !strings.HasPrefix(path, "/") {
		return &ValidationError{newArcaError("VALIDATION_ERROR",
			"Path must start with '/'. Got: \""+path+"\". Use a trailing slash for aggregation (e.g. '/users/alice/') or an exact path for a single object (e.g. '/users/alice/main').", "")}
	}
	return nil
}

func strPtr(s string) *string { return &s }
