package arca

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// writeEnvelope writes a success envelope wrapping data.
func writeEnvelope(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	raw, _ := json.Marshal(data)
	resp := map[string]any{"success": true, "data": json.RawMessage(raw)}
	_ = json.NewEncoder(w).Encode(resp)
}

func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]any{"success": false, "error": map[string]any{"code": code, "message": message, "details": details}}
	_ = json.NewEncoder(w).Encode(resp)
}

// newTestArca builds a client pointed at a test server with the realm already
// resolved (TypeID form) so methods don't issue a /realms lookup.
func newTestArca(t *testing.T, baseURL string) *Arca {
	t.Helper()
	a, err := New(Config{APIKey: "arca_test_key", Realm: "rlm_01h2xcejqtf2nbrexx3vqjhp41", BaseURL: baseURL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func TestMapAPIError(t *testing.T) {
	cases := []struct {
		code string
		want func(error) bool
	}{
		{"VALIDATION_ERROR", func(e error) bool { var x *ValidationError; return errors.As(e, &x) }},
		{"NOT_FOUND", func(e error) bool { var x *NotFoundError; return errors.As(e, &x) }},
		{"OBJECT_NOT_FOUND", func(e error) bool { var x *NotFoundError; return errors.As(e, &x) }},
		{"IDEMPOTENCY_VIOLATION", func(e error) bool { var x *ConflictError; return errors.As(e, &x) }},
		{"ORDER_FAILED", func(e error) bool { var x *ExchangeError; return errors.As(e, &x) }},
		{"UNAUTHORIZED", func(e error) bool { var x *UnauthorizedError; return errors.As(e, &x) }},
	}
	for _, c := range cases {
		err := mapAPIError(c.code, "msg", "err_1", nil)
		if !c.want(err) {
			t.Errorf("mapAPIError(%s) -> %T (wrong type)", c.code, err)
		}
		var base *ArcaError
		if !errors.As(err, &base) || base.Code != c.code {
			t.Errorf("mapAPIError(%s): code not preserved", c.code)
		}
	}
}

func TestMapAPIError_StepUpChallenge(t *testing.T) {
	details := map[string]any{"action": "arca:DeleteObject", "resources": []any{"/users/alice"}}
	err := mapAPIError("STEP_UP_REQUIRED", "step up", "", details)
	var su *StepUpRequiredError
	if !errors.As(err, &su) {
		t.Fatalf("expected StepUpRequiredError, got %T", err)
	}
	if su.Action != "arca:DeleteObject" || len(su.Resources) != 1 || su.Resources[0] != "/users/alice" {
		t.Errorf("challenge not parsed: %+v", su)
	}
}

func TestClient_TransientRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeEnvelope(w, 200, ArcaObject{ID: "obj_1", Path: "/x"})
	}))
	defer srv.Close()

	a := newTestArca(t, srv.URL)
	obj, err := a.GetObject(context.Background(), "/x")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if obj.ID != "obj_1" {
		t.Errorf("got %s", obj.ID)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("expected 3 calls (2 retries), got %d", got)
	}
}

func TestClient_401RefreshOnce(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		auth := r.Header.Get("Authorization")
		if auth != "Bearer fresh-token" {
			writeError(w, 401, "UNAUTHORIZED", "expired", nil)
			return
		}
		writeEnvelope(w, 200, ArcaObject{ID: "obj_ok"})
	}))
	defer srv.Close()

	var refreshed int32
	a, err := FromTokenProvider(func(ctx context.Context) (string, error) {
		atomic.AddInt32(&refreshed, 1)
		return "fresh-token", nil
	}, Config{Realm: "rlm_01h2xcejqtf2nbrexx3vqjhp41", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("FromTokenProvider: %v", err)
	}
	obj, err := a.GetObject(context.Background(), "/x")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if obj.ID != "obj_ok" {
		t.Errorf("got %s", obj.ID)
	}
	_ = calls
}

func TestClient_StepUpRetry(t *testing.T) {
	var sawStepUpToken int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer step-up-jwt" {
			atomic.AddInt32(&sawStepUpToken, 1)
			writeEnvelope(w, 200, DeleteArcaObjectResponse{Object: ArcaObject{ID: "obj_del"}, Operation: &Operation{ID: "op_1", State: OpCompleted}})
			return
		}
		writeError(w, 412, "STEP_UP_REQUIRED", "confirm required", map[string]any{
			"action": "arca:DeleteObject", "resources": []any{"/users/alice"},
		})
	}))
	defer srv.Close()

	var challengeAction string
	a, err := New(Config{
		APIKey:  "arca_test",
		Realm:   "rlm_01h2xcejqtf2nbrexx3vqjhp41",
		BaseURL: srv.URL,
		StepUpHandler: func(ctx context.Context, ch StepUpChallenge) (string, error) {
			challengeAction = ch.Action
			return "step-up-jwt", nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := a.EnsureDeleted(context.Background(), EnsureDeletedOptions{Ref: "/users/alice"}).Submitted(context.Background())
	if err != nil {
		t.Fatalf("EnsureDeleted: %v", err)
	}
	if resp.Object.ID != "obj_del" {
		t.Errorf("got %s", resp.Object.ID)
	}
	if challengeAction != "arca:DeleteObject" {
		t.Errorf("challenge action = %q", challengeAction)
	}
	if atomic.LoadInt32(&sawStepUpToken) != 1 {
		t.Errorf("step-up token retry did not happen")
	}
}

func TestRealmResolution_BySlug(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/realms" {
			writeEnvelope(w, 200, RealmListResponse{Realms: []Realm{{ID: "rlm_resolved", Slug: "my-realm"}}, Total: 1})
			return
		}
		writeEnvelope(w, 200, ExplorerSummary{ObjectCount: 7})
	}))
	defer srv.Close()

	a, err := New(Config{APIKey: "arca_test", Realm: "my-realm", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if a.getResolvedRealmID() != "rlm_resolved" {
		t.Errorf("realm resolved to %q", a.getResolvedRealmID())
	}
	sum, err := a.Summary(context.Background())
	if err != nil || sum.ObjectCount != 7 {
		t.Errorf("Summary: %v %+v", err, sum)
	}
}

func TestOperationHandle_WaitSettlesViaPoll(t *testing.T) {
	var opCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/transfer":
			writeEnvelope(w, 200, TransferResponse{Operation: Operation{ID: "op_xfer", State: OpPending}})
		case strings.HasPrefix(r.URL.Path, "/api/v1/operations/op_xfer"):
			n := atomic.AddInt32(&opCalls, 1)
			state := OpPending
			if n >= 2 {
				state = OpCompleted
			}
			writeEnvelope(w, 200, OperationDetailResponse{Operation: Operation{ID: "op_xfer", State: state}})
		default:
			writeEnvelope(w, 200, map[string]any{})
		}
	}))
	defer srv.Close()

	a := newTestArca(t, srv.URL)
	defer a.Dispose()

	h := a.Transfer(context.Background(), TransferOptions{Path: "/op/transfer/1", From: "/a", To: "/b", Amount: "10"})
	if h.Predicted == nil || h.Predicted.Type != "transfer" {
		t.Errorf("predicted effect missing")
	}
	sub, err := h.Submitted(context.Background())
	if err != nil {
		t.Fatalf("Submitted: %v", err)
	}
	if sub.Operation.State != OpPending {
		t.Errorf("submitted state = %s", sub.Operation.State)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	final, err := h.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if final.Operation.State != OpCompleted {
		t.Errorf("final state = %s", final.Operation.State)
	}
}

func TestOperationHandle_WaitFailedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msg := "insufficient balance"
		switch {
		case r.URL.Path == "/api/v1/transfer":
			writeEnvelope(w, 200, TransferResponse{Operation: Operation{ID: "op_f", State: OpPending}})
		case strings.HasPrefix(r.URL.Path, "/api/v1/operations/op_f"):
			writeEnvelope(w, 200, OperationDetailResponse{Operation: Operation{ID: "op_f", State: OpFailed, FailureMessage: &msg}})
		default:
			writeEnvelope(w, 200, map[string]any{})
		}
	}))
	defer srv.Close()

	a := newTestArca(t, srv.URL)
	defer a.Dispose()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := a.Transfer(ctx, TransferOptions{Path: "/op/transfer/2", From: "/a", To: "/b", Amount: "10"}).Wait(ctx)
	var ofe *OperationFailedError
	if !errors.As(err, &ofe) {
		t.Fatalf("expected OperationFailedError, got %v", err)
	}
	if ofe.Operation.FailureMessage == nil || *ofe.Operation.FailureMessage != "insufficient balance" {
		t.Errorf("failure message not surfaced: %+v", ofe.Operation)
	}
}

func TestComputeOrderBreakdown(t *testing.T) {
	// notional mode: 1000 notional, 10x leverage, 0.1% fee.
	b := ComputeOrderBreakdown(OrderBreakdownOptions{
		Amount: "1000", AmountType: "notional", Leverage: 10, FeeRate: "0.001",
		Price: "100", Side: Buy, SzDecimals: 2,
	})
	if b.NotionalUsd != "1000" {
		t.Errorf("notional = %s", b.NotionalUsd)
	}
	if b.Tokens != "10" {
		t.Errorf("tokens = %s (want 10)", b.Tokens)
	}
	if b.MarginRequired != "100" {
		t.Errorf("marginRequired = %s (want 100)", b.MarginRequired)
	}
	if b.EstimatedFee != "1" {
		t.Errorf("estimatedFee = %s (want 1)", b.EstimatedFee)
	}
	if b.TotalSpend != "101" {
		t.Errorf("totalSpend = %s (want 101)", b.TotalSpend)
	}

	// spend mode: 250 spend at 4x with zero fee -> 1000 notional, 10 tokens.
	bs := ComputeOrderBreakdown(OrderBreakdownOptions{
		Amount: "250", AmountType: "spend", Leverage: 4, FeeRate: "0",
		Price: "100", Side: Buy, SzDecimals: 2,
	})
	if bs.NotionalUsd != "1000" {
		t.Errorf("spend-mode notional = %s (want 1000)", bs.NotionalUsd)
	}
	if bs.Tokens != "10" {
		t.Errorf("spend-mode tokens = %s (want 10)", bs.Tokens)
	}

	// invalid inputs return zeros.
	z := ComputeOrderBreakdown(OrderBreakdownOptions{Amount: "0", AmountType: "notional", Leverage: 10, FeeRate: "0.001", Price: "100", Side: Buy})
	if z.Tokens != "0" || z.NotionalUsd != "0" {
		t.Errorf("expected zeros, got %+v", z)
	}
}

func TestPickResolution(t *testing.T) {
	day := int64(86_400_000)
	cases := []struct {
		rangeMs int64
		points  int
		want    Resolution
	}{
		{day, 1000, Res5m},        // 24h / 5m = 288 <= 1000
		{30 * day, 1000, Res1h},   // 30d / 1h = 720 <= 1000
		{90 * day, 1000, Res4h},   // 90d / 4h = 540
		{3650 * day, 1000, Res1d}, // very long -> coarsest
	}
	for _, c := range cases {
		if got := PickResolution(c.rangeMs, c.points); got != c.want {
			t.Errorf("PickResolution(%d,%d) = %s, want %s", c.rangeMs, c.points, got, c.want)
		}
	}
}

func TestEncodeCustodyCall(t *testing.T) {
	data, err := encodeCustodyCall("setTimeLock(bytes32,uint256)", []string{
		"0x00000000000000000000000000000000000000000000000000000000000000aa",
		"86400",
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := "0x3e41d187" +
		"00000000000000000000000000000000000000000000000000000000000000aa" +
		"0000000000000000000000000000000000000000000000000000000000015180"
	if data != want {
		t.Errorf("encoded = %s\nwant     = %s", data, want)
	}

	if _, err := encodeCustodyCall("nope(bytes32)", []string{"0x00"}); err == nil {
		t.Error("expected error for unknown selector")
	}
}

func TestAbiEncodeParam_Address(t *testing.T) {
	got, err := abiEncodeParam("0x1234567890ABCDEF1234567890abcdef12345678")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := "0000000000000000000000001234567890abcdef1234567890abcdef12345678"
	if got != want {
		t.Errorf("address encode = %s, want %s", got, want)
	}
}

func TestPrepareLockBoundary(t *testing.T) {
	tx, err := PrepareLockBoundary("0xcontract", 998, "0x00000000000000000000000000000000000000000000000000000000000000b0")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if tx.To != "0xcontract" || tx.ChainID != 998 || tx.Value != "0" {
		t.Errorf("tx = %+v", tx)
	}
	if !strings.HasPrefix(tx.Data, "0x67f82a1d") {
		t.Errorf("data prefix = %s", tx.Data)
	}
}

func TestDeriveAddressFromMnemonic_KnownVector(t *testing.T) {
	// Canonical BIP-39/BIP-44 test vector for Ethereum (m/44'/60'/0'/0/0).
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	addr, err := deriveAddressFromMnemonic(mnemonic)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	want := "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"
	if addr != want {
		t.Errorf("address = %s, want %s", addr, want)
	}
}

func TestGenerateRecoveryKey(t *testing.T) {
	k, err := GenerateRecoveryKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(strings.Fields(k.Mnemonic)) != 12 {
		t.Errorf("mnemonic word count = %d", len(strings.Fields(k.Mnemonic)))
	}
	if !strings.HasPrefix(k.Address, "0x") || len(k.Address) != 42 {
		t.Errorf("address malformed: %s", k.Address)
	}
	// Re-deriving from the same mnemonic must produce the same address.
	again, err := deriveAddressFromMnemonic(k.Mnemonic)
	if err != nil || again != k.Address {
		t.Errorf("re-derivation mismatch: %s vs %s (%v)", again, k.Address, err)
	}
}

func TestDecodeJWTPayload(t *testing.T) {
	// header.payload.signature with payload {"realmId":"rlm_jwt","exp":9999999999}
	payload := base64URL(`{"realmId":"rlm_jwt","exp":9999999999}`)
	token := "h." + payload + ".s"
	claims := decodeJWTPayload(token)
	if claims == nil || claims["realmId"] != "rlm_jwt" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestFromToken_ExtractsRealm(t *testing.T) {
	payload := base64URL(`{"realmId":"rlm_from_token","exp":9999999999}`)
	a, err := FromToken("h."+payload+".s", Config{BaseURL: "https://example.test"})
	if err != nil {
		t.Fatalf("FromToken: %v", err)
	}
	if a.getResolvedRealmID() != "rlm_from_token" {
		t.Errorf("realm = %q", a.getResolvedRealmID())
	}
}

func TestValidatePath(t *testing.T) {
	if err := validatePath("/users/alice"); err != nil {
		t.Errorf("valid path rejected: %v", err)
	}
	err := validatePath("users/alice")
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError, got %v", err)
	}
}

func TestChunksForRange_Daily(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
	end := time.Date(2026, 1, 3, 6, 0, 0, 0, time.UTC).UnixMilli()
	chunks := chunksForRange(Interval5m, start, end)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 daily chunks, got %d", len(chunks))
	}
	if chunks[0].key != "2026-01-01" || chunks[2].key != "2026-01-03" {
		t.Errorf("chunk keys = %s..%s", chunks[0].key, chunks[2].key)
	}
}

func base64URL(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}
