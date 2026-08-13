package arca

import (
	"errors"
	"fmt"
)

func isUnauthorized(err error) bool {
	var e *UnauthorizedError
	return errors.As(err, &e)
}

func isForbidden(err error) bool {
	var e *ForbiddenError
	return errors.As(err, &e)
}

func isAuthRejection(err error) bool {
	return isUnauthorized(err) || isForbidden(err)
}

func asUnauthorized(err error) *UnauthorizedError {
	var e *UnauthorizedError
	if errors.As(err, &e) {
		return e
	}
	return &UnauthorizedError{newArcaError("UNAUTHORIZED", err.Error(), "")}
}

func asStepUp(err error, target **StepUpRequiredError) bool {
	return errors.As(err, target)
}

func asStepUpCancelled(err error, target **StepUpCancelledError) bool {
	return errors.As(err, target)
}

func asError[T error](err error, target *T) bool {
	return errors.As(err, target)
}

// ArcaError is the base error type for all SDK errors. Every error carries a
// machine-readable Code and an optional ErrorID (a server-side correlation id
// that can be quoted to support).
//
// Typed errors below embed *ArcaError, so callers can either switch on the
// concrete type via errors.As, or read the Code field directly:
//
//	var nf *arca.NotFoundError
//	if errors.As(err, &nf) { ... }
//
//	var ae *arca.ArcaError
//	if errors.As(err, &ae) && ae.Code == "IDEMPOTENCY_VIOLATION" { ... }
type ArcaError struct {
	Code    string
	Message string
	ErrorID string
}

func (e *ArcaError) Error() string {
	if e.ErrorID != "" {
		return fmt.Sprintf("%s: %s (errorId=%s)", e.Code, e.Message, e.ErrorID)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func newArcaError(code, message, errorID string) *ArcaError {
	return &ArcaError{Code: code, Message: message, ErrorID: errorID}
}

// ValidationError is returned when the API rejects request parameters (HTTP 400).
type ValidationError struct{ *ArcaError }

// UnauthorizedError is returned when authentication is missing or invalid (HTTP 401).
type UnauthorizedError struct{ *ArcaError }

// ForbiddenError is returned when the server refuses a request for lack of
// permission (HTTP 403) — Code is "FORBIDDEN" (action not granted on the
// resource) or "REALM_SCOPE_MISMATCH" (token locked to a different realm).
//
// On a token-provider client a 403 commonly means the cached token is still
// valid but scoped to a different identity than the one the provider would
// now mint for (e.g. the app switched signed-in users). The SDK reacts by
// re-invoking the provider once and retrying; an unrecoverable 403 is
// surfaced through OnAuthError so integrators can tear down and rebuild.
type ForbiddenError struct{ *ArcaError }

// NotFoundError is returned when a resource does not exist (HTTP 404).
type NotFoundError struct{ *ArcaError }

// ConflictError is returned on a 409 conflict — a well-formed request the
// current server/venue state can't satisfy. Covers idempotency conflicts
// (same path, different inputs) and venue refusals, where Code carries the
// specific reason:
//
//   - "NO_LIQUIDITY" — empty book side; retry or use a marketable limit
//   - "MARKET_DELISTED" — market delisted, open positions were settled by the
//     venue, no new orders accepted
//   - "MARKET_NOT_TRADABLE" — market exists but is halted or not yet live
//   - "MARKET_NOT_USDC_COLLATERAL" — position-opening order on a market whose
//     collateral is not USDC
//   - "VENUE_RATE_LIMITED" — the account exhausted its request allowance at the
//     venue. On Hyperliquid that allowance is earned by cumulative volume
//     traded, not elapsed time, so waiting does not restore it and each retry
//     spends budget the account has not earned; Message carries the remedy
//   - "ORDER_FAILED" — the venue refused the order for a reason with no
//     narrower code; Message carries its verbatim text
//
// None of these are retryable as-is: the venue evaluated the request and said
// no, so the same request gets the same answer.
type ConflictError struct{ *ArcaError }

// InternalError is returned when the server hits an unexpected error (HTTP 500).
type InternalError struct{ *ArcaError }

// ExchangeError is returned when the request could not be delivered to, or a
// usable answer read from, the upstream exchange (HTTP 502) — a connection
// reset, a timeout, an unparseable response. It is a transport fault, not a
// verdict, so retrying is reasonable. A refusal *by* the venue is a
// ConflictError or ValidationError carrying the venue's own reason.
type ExchangeError struct{ *ArcaError }

// OperationSnapshot is the minimal operation view carried on operation errors.
type OperationSnapshot struct {
	ID             string
	State          string
	Outcome        *string
	FailureMessage *string
}

// OperationFailedError is returned when an awaited operation reaches a
// non-success terminal state (failed or expired). The Operation snapshot holds
// the failure detail; Operation.Outcome carries the raw outcome JSON for
// programmatic inspection.
type OperationFailedError struct {
	*ArcaError
	Operation OperationSnapshot
}

// OperationStalledError is returned when waiting for an operation times out
// before it reaches a terminal state. Distinct from OperationFailedError: the
// operation may still complete or fail later. Operation holds the last known
// snapshot (best effort), and TimeoutMS is the budget that elapsed.
type OperationStalledError struct {
	*ArcaError
	OperationID string
	TimeoutMS   int64
	Operation   *OperationSnapshot
}

// StepUpChallenge is the structured payload accompanying a 412 STEP_UP_REQUIRED
// response. Action is the gated permission action (e.g. "arca:DeleteObject");
// Resources is the list of resource identifiers the step-up token must
// authorize.
type StepUpChallenge struct {
	Action    string
	Resources []string
}

// StepUpRequiredError is returned when the server requires browser
// confirmation for a destructive action on a production realm (HTTP 412 with
// code STEP_UP_REQUIRED). In normal use this is intercepted by a StepUpHandler
// and never surfaces to caller code; it propagates only when no handler is
// wired.
type StepUpRequiredError struct {
	*ArcaError
	Action    string
	Resources []string
}

// StepUpCancelledError is returned when a StepUpHandler signals that the user
// cancelled the confirmation flow (or it expired / errored).
type StepUpCancelledError struct{ *ArcaError }

// Unwrap exposes the embedded *ArcaError so errors.As(err, &arca.ArcaError{})
// reaches the base error (Code/Message/ErrorID) from any typed error.
func (e *ValidationError) Unwrap() error       { return e.ArcaError }
func (e *UnauthorizedError) Unwrap() error     { return e.ArcaError }
func (e *ForbiddenError) Unwrap() error        { return e.ArcaError }
func (e *NotFoundError) Unwrap() error         { return e.ArcaError }
func (e *ConflictError) Unwrap() error         { return e.ArcaError }
func (e *InternalError) Unwrap() error         { return e.ArcaError }
func (e *ExchangeError) Unwrap() error         { return e.ArcaError }
func (e *OperationFailedError) Unwrap() error  { return e.ArcaError }
func (e *OperationStalledError) Unwrap() error { return e.ArcaError }
func (e *StepUpRequiredError) Unwrap() error   { return e.ArcaError }
func (e *StepUpCancelledError) Unwrap() error  { return e.ArcaError }

func newOperationFailedError(op OperationSnapshot) *OperationFailedError {
	msg := "This operation could not be completed."
	if op.FailureMessage != nil && *op.FailureMessage != "" {
		msg = *op.FailureMessage
	}
	return &OperationFailedError{ArcaError: newArcaError("OPERATION_FAILED", msg, ""), Operation: op}
}

func newOperationStalledError(operationID string, timeoutMS int64, op *OperationSnapshot) *OperationStalledError {
	lastState := "unknown"
	if op != nil {
		lastState = op.State
	}
	msg := fmt.Sprintf("Timed out waiting for operation %s to reach a terminal state after %dms (last known state: %s)", operationID, timeoutMS, lastState)
	return &OperationStalledError{
		ArcaError:   newArcaError("OPERATION_STALLED", msg, ""),
		OperationID: operationID,
		TimeoutMS:   timeoutMS,
		Operation:   op,
	}
}

// apiErrorDetails mirrors the server's error.details map for step-up parsing.
func parseStepUpChallenge(details map[string]any) *StepUpChallenge {
	if details == nil {
		return nil
	}
	action, ok := details["action"].(string)
	if !ok {
		return nil
	}
	rawResources, ok := details["resources"].([]any)
	if !ok {
		return nil
	}
	resources := make([]string, 0, len(rawResources))
	for _, r := range rawResources {
		if s, ok := r.(string); ok {
			resources = append(resources, s)
		}
	}
	return &StepUpChallenge{Action: action, Resources: resources}
}

// mapAPIError maps an API error envelope to a typed SDK error.
func mapAPIError(code, message, errorID string, details map[string]any) error {
	base := newArcaError(code, message, errorID)
	switch code {
	case "VALIDATION_ERROR":
		return &ValidationError{base}
	case "UNAUTHORIZED":
		return &UnauthorizedError{base}
	case "FORBIDDEN", "REALM_SCOPE_MISMATCH":
		return &ForbiddenError{base}
	case "NOT_FOUND", "USER_NOT_FOUND", "REALM_NOT_FOUND", "OBJECT_NOT_FOUND",
		"ORG_NOT_FOUND", "ORDER_NOT_FOUND", "ACCOUNT_NOT_FOUND", "MEMBER_NOT_FOUND",
		"PROFILE_NOT_FOUND", "INVITATION_NOT_FOUND":
		return &NotFoundError{base}
	case "CONFLICT", "ALREADY_EXISTS", "ALREADY_MEMBER", "ALREADY_DELETED",
		"DUPLICATE_REALM", "ALREADY_REVOKED", "IDEMPOTENCY_VIOLATION",
		// Order-placement conflicts (409): the venue evaluated a well-formed
		// request and refused it. NO_LIQUIDITY = empty book side right now
		// (retry / marketable limit); MARKET_DELISTED = market gone, positions
		// were settled by the venue; MARKET_NOT_TRADABLE = halted or not yet
		// live; VENUE_RATE_LIMITED = the account's venue request allowance is
		// spent (volume-earned on HL, so waiting does not help);
		// ORDER_FAILED = a refusal with no narrower code, verbatim venue
		// text in Message. The specific code stays on base.Code.
		"NO_LIQUIDITY", "MARKET_DELISTED", "MARKET_NOT_TRADABLE",
		"MARKET_NOT_USDC_COLLATERAL", "VENUE_RATE_LIMITED", "ORDER_FAILED":
		return &ConflictError{base}
	case "INTERNAL_ERROR":
		return &InternalError{base}
	case "EXCHANGE_ERROR", "EXCHANGE_UNAVAILABLE", "INVALID_REQUEST":
		return &ExchangeError{base}
	case "STEP_UP_REQUIRED":
		if challenge := parseStepUpChallenge(details); challenge != nil {
			return &StepUpRequiredError{ArcaError: base, Action: challenge.Action, Resources: challenge.Resources}
		}
		return base
	default:
		return base
	}
}
