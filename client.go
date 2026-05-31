package arca

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// StepUpHandler is invoked by the SDK when a request fails with HTTP 412 +
// STEP_UP_REQUIRED. The handler is responsible for interactively obtaining a
// single-use step-up JWT (typically by showing a confirmation flow that
// creates and approves a step-up request). Returning a token causes the SDK to
// retry the original request exactly once with that token as the bearer — the
// original credential is restored afterward and the step-up token is never
// persisted. Returning an error surfaces the original 412 as a
// *StepUpCancelledError.
type StepUpHandler func(ctx context.Context, challenge StepUpChallenge) (string, error)

type credentialType string

const (
	credAPIKey credentialType = "apiKey"
	credToken  credentialType = "token"
)

const (
	maxRetries        = 2
	retryBaseDelay    = 250 * time.Millisecond
	retryMaxDelay     = 2 * time.Second
	defaultHTTPTimout = 60 * time.Second
)

var transientStatuses = map[int]bool{502: true, 503: true, 504: true}

// apiResponse is the envelope every Arca API endpoint returns.
type apiResponse struct {
	Success bool              `json:"success"`
	Data    json.RawMessage   `json:"data"`
	Error   *apiResponseError `json:"error"`
}

type apiResponseError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	ErrorID string         `json:"errorId"`
	Details map[string]any `json:"details"`
}

type httpClient struct {
	mu         sync.RWMutex
	credential string
	credType   credentialType
	baseURL    string
	http       *http.Client

	onUnauthorized func(ctx context.Context) (string, error)
	onAuthError    func(error)
	headerHook     func() map[string]string

	stepUpMu       sync.Mutex
	stepUpHandler  StepUpHandler
	stepUpInFlight bool
}

type clientConfig struct {
	credential     string
	credType       credentialType
	baseURL        string
	httpClient     *http.Client
	onUnauthorized func(ctx context.Context) (string, error)
	onAuthError    func(error)
	stepUpHandler  StepUpHandler
	headerHook     func() map[string]string
}

func newHTTPClient(cfg clientConfig) *httpClient {
	hc := cfg.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultHTTPTimout}
	}
	return &httpClient{
		credential:     cfg.credential,
		credType:       cfg.credType,
		baseURL:        strings.TrimRight(cfg.baseURL, "/"),
		http:           hc,
		onUnauthorized: cfg.onUnauthorized,
		onAuthError:    cfg.onAuthError,
		stepUpHandler:  cfg.stepUpHandler,
		headerHook:     cfg.headerHook,
	}
}

func (c *httpClient) updateCredential(cred string) {
	c.mu.Lock()
	c.credential = cred
	c.mu.Unlock()
}

func (c *httpClient) getCredential() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.credential
}

func (c *httpClient) setStepUpHandler(h StepUpHandler) {
	c.stepUpMu.Lock()
	c.stepUpHandler = h
	c.stepUpMu.Unlock()
}

func (c *httpClient) get(ctx context.Context, path string, params url.Values, out any) error {
	return c.executeWithAuthRetry(ctx, http.MethodGet, path, params, nil, out)
}

func (c *httpClient) post(ctx context.Context, path string, body, out any) error {
	return c.executeWithAuthRetry(ctx, http.MethodPost, path, nil, body, out)
}

func (c *httpClient) patch(ctx context.Context, path string, params url.Values, body, out any) error {
	return c.executeWithAuthRetry(ctx, http.MethodPatch, path, params, body, out)
}

func (c *httpClient) put(ctx context.Context, path string, body, out any) error {
	return c.executeWithAuthRetry(ctx, http.MethodPut, path, nil, body, out)
}

func (c *httpClient) delete(ctx context.Context, path string, out any) error {
	return c.executeWithAuthRetry(ctx, http.MethodDelete, path, nil, nil, out)
}

// executeWithAuthRetry wraps requestWithRetry with a single 401 refresh and a
// single 412 step-up retry. Mirrors the TS client's executeWithAuthRetry.
func (c *httpClient) executeWithAuthRetry(ctx context.Context, method, path string, params url.Values, body, out any) error {
	err := c.requestWithRetry(ctx, method, path, params, body, out)
	if err == nil {
		return nil
	}

	if isUnauthorized(err) && c.onUnauthorized != nil {
		newCred, refreshErr := c.onUnauthorized(ctx)
		if refreshErr != nil {
			if c.onAuthError != nil {
				c.onAuthError(asUnauthorized(refreshErr))
			}
			return refreshErr
		}
		c.updateCredential(newCred)
		return c.requestWithRetry(ctx, method, path, params, body, out)
	}
	if isUnauthorized(err) {
		if c.onAuthError != nil {
			c.onAuthError(err)
		}
		return err
	}

	var stepUp *StepUpRequiredError
	if asStepUp(err, &stepUp) {
		c.stepUpMu.Lock()
		handler := c.stepUpHandler
		inFlight := c.stepUpInFlight
		c.stepUpMu.Unlock()
		if handler != nil && !inFlight {
			return c.runStepUpRetry(ctx, stepUp, handler, method, path, params, body, out)
		}
	}
	return err
}

func (c *httpClient) runStepUpRetry(ctx context.Context, challengeErr *StepUpRequiredError, handler StepUpHandler, method, path string, params url.Values, body, out any) error {
	c.stepUpMu.Lock()
	c.stepUpInFlight = true
	c.stepUpMu.Unlock()
	defer func() {
		c.stepUpMu.Lock()
		c.stepUpInFlight = false
		c.stepUpMu.Unlock()
	}()

	token, err := handler(ctx, StepUpChallenge{Action: challengeErr.Action, Resources: challengeErr.Resources})
	if err != nil {
		var cancelled *StepUpCancelledError
		if asStepUpCancelled(err, &cancelled) {
			return err
		}
		return &StepUpCancelledError{newArcaError("STEP_UP_CANCELLED", err.Error(), "")}
	}

	original := c.getCredential()
	c.updateCredential(token)
	defer c.updateCredential(original)
	return c.requestWithRetry(ctx, method, path, params, body, out)
}

func (c *httpClient) requestWithRetry(ctx context.Context, method, path string, params url.Values, body, out any) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := c.requestOnce(ctx, method, path, params, body, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isTransient(err) || attempt == maxRetries {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay(attempt)):
		}
	}
	return lastErr
}

func (c *httpClient) requestOnce(ctx context.Context, method, path string, params url.Values, body, out any) error {
	u, err := c.buildURL(path, params)
	if err != nil {
		return err
	}

	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cred := c.getCredential(); cred != "" {
		req.Header.Set("Authorization", "Bearer "+cred)
	}
	if c.headerHook != nil {
		for k, v := range c.headerHook() {
			if v != "" {
				req.Header.Set(k, v)
			}
		}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Network failure — treated as transient.
		return &transientError{err: err}
	}
	defer resp.Body.Close()

	if transientStatuses[resp.StatusCode] {
		io.Copy(io.Discard, resp.Body)
		return &transientError{status: resp.StatusCode}
	}

	return c.unwrap(resp, out)
}

func (c *httpClient) unwrap(resp *http.Response, out any) error {
	text, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var env apiResponse
	if err := json.Unmarshal(text, &env); err != nil {
		preview := string(text)
		if len(preview) > 200 {
			preview = preview[:200] + "…"
		}
		return newArcaError("NON_JSON_RESPONSE", fmt.Sprintf("Server returned non-JSON response (HTTP %d): %s", resp.StatusCode, preview), "")
	}

	if !env.Success || len(env.Data) == 0 || string(env.Data) == "null" {
		if resp.StatusCode == http.StatusUnauthorized {
			msg := "Invalid or expired authentication"
			var eid string
			if env.Error != nil {
				if env.Error.Message != "" {
					msg = env.Error.Message
				}
				eid = env.Error.ErrorID
			}
			return &UnauthorizedError{newArcaError("UNAUTHORIZED", msg, eid)}
		}
		if env.Error != nil {
			return mapAPIError(env.Error.Code, env.Error.Message, env.Error.ErrorID, env.Error.Details)
		}
		return newArcaError("UNKNOWN", fmt.Sprintf("Request failed with status %d", resp.StatusCode), "")
	}

	if out != nil {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("arca: decode response: %w", err)
		}
	}
	return nil
}

func (c *httpClient) buildURL(path string, params url.Values) (string, error) {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return "", err
	}
	if len(params) > 0 {
		q := u.Query()
		for k, vs := range params {
			for _, v := range vs {
				q.Set(k, v)
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func retryDelay(attempt int) time.Duration {
	ceil := retryBaseDelay * (1 << attempt)
	if ceil > retryMaxDelay {
		ceil = retryMaxDelay
	}
	return time.Duration(rand.Int64N(int64(ceil)))
}

// transientError marks a retryable failure (network error or 502/503/504).
type transientError struct {
	status int
	err    error
}

func (e *transientError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("transient network error: %v", e.err)
	}
	return fmt.Sprintf("transient HTTP %d", e.status)
}

func (e *transientError) Unwrap() error { return e.err }

func isTransient(err error) bool {
	var te *transientError
	return asError(err, &te)
}
