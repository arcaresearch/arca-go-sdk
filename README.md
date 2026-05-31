# Arca Go SDK

`github.com/arcaresearch/arca-go-sdk` — a Go SDK for the Arca platform
(accounts, payments, perpetuals trading, real-time streaming, and audit
trails). It is a hand-written port of the [TypeScript SDK](../typescript) with
idiomatic Go ergonomics: `context.Context` on every call, typed errors usable
with `errors.As`, generic operation handles, and channel/callback watch
streams.

## Install

```bash
go get github.com/arcaresearch/arca-go-sdk
```

Requires Go 1.23+.

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"

	arca "github.com/arcaresearch/arca-go-sdk"
)

func main() {
	ctx := context.Background()

	// Backend: authenticate with an API key.
	client, err := arca.New(arca.Config{APIKey: "arca_...", Realm: "my-realm"})
	if err != nil {
		log.Fatal(err)
	}
	if err := client.Ready(ctx); err != nil { // resolve realm slug -> id
		log.Fatal(err)
	}

	// Create two wallets (Wait blocks until the create operation settles).
	if _, err := client.EnsureDenominatedArca(ctx, arca.EnsureDenominatedArcaOptions{Ref: "/users/alice/wallet"}).Wait(ctx); err != nil {
		log.Fatal(err)
	}
	if _, err := client.EnsureDenominatedArca(ctx, arca.EnsureDenominatedArcaOptions{Ref: "/users/bob/wallet"}).Wait(ctx); err != nil {
		log.Fatal(err)
	}

	// Fund Alice (dev/test only — use CreatePaymentLink in production).
	if _, err := client.FundAccount(ctx, arca.FundAccountOptions{ArcaRef: "/users/alice/wallet", Amount: "1000"}).Wait(ctx); err != nil {
		log.Fatal(err)
	}

	// Transfer $50 Alice -> Bob. The nonce path is the idempotency key.
	nonce, err := client.Nonce(ctx, "/op/transfer/alice-to-bob/001")
	if err != nil {
		log.Fatal(err)
	}
	if _, err := client.Transfer(ctx, arca.TransferOptions{
		Path: nonce.Path, From: "/users/alice/wallet", To: "/users/bob/wallet", Amount: "50",
	}).Wait(ctx); err != nil {
		log.Fatal(err)
	}

	balances, _ := client.GetBalancesByPath(ctx, "/users/bob/wallet")
	fmt.Println("Bob settled:", balances[0].Settled) // 50
}
```

## Authentication

Three credential modes, mirroring the TypeScript SDK:

```go
// API key (server-side).
arca.New(arca.Config{APIKey: "arca_...", Realm: "my-realm"})

// Scoped JWT (the realm is read from the token claims when present).
arca.FromToken(jwt, arca.Config{})

// Auto-refreshing token. The provider is called on first use, ~30s before
// expiry, and on HTTP 401.
arca.FromTokenProvider(func(ctx context.Context) (string, error) {
	return fetchTokenFromMyBackend(ctx)
}, arca.Config{Realm: "my-realm"})
```

### Step-up auth

Destructive actions on production realms return HTTP 412 `STEP_UP_REQUIRED`.
Register a handler to obtain a single-use step-up token; the SDK retries the
original request once with it and never persists it:

```go
client, _ := arca.New(arca.Config{
	APIKey: "arca_...", Realm: "prod",
	StepUpHandler: func(ctx context.Context, ch arca.StepUpChallenge) (string, error) {
		return confirmInBrowser(ctx, ch.Action, ch.Resources) // returns the step-up JWT
	},
})
```

## Operation handles

Mutation methods (`Transfer`, `EnsureDenominatedArca`, `PlaceOrder`, …) return a
handle immediately; the HTTP request runs in the background:

```go
h := client.Transfer(ctx, opts)
resp, _ := h.Submitted(ctx) // HTTP response, before settlement
final, err := h.Wait(ctx)   // blocks until the operation reaches a terminal state
```

`Wait` returns `*arca.OperationFailedError` on failure and
`*arca.OperationStalledError` on timeout. `PlaceOrder` / `ClosePosition` return
an `*OrderHandle` with `Filled`, `OnFill`, `FillSummary`, and `Cancel`.

## Errors

```go
_, err := client.GetObject(ctx, "/nope")
var notFound *arca.NotFoundError
if errors.As(err, &notFound) { /* ... */ }

var ae *arca.ArcaError
if errors.As(err, &ae) && ae.Code == "IDEMPOTENCY_VIOLATION" { /* ... */ }
```

## Real-time streaming

Watch streams expose `OnUpdate(cb) func()`, an `Updates() <-chan T` channel,
`Ready(ctx)`, `State()`, and `Close()`:

```go
prices, _ := client.WatchPrices(ctx, &arca.WatchPricesOptions{Coins: []string{"hl:BTC"}})
defer prices.Close()
px, _ := prices.Get("hl:BTC") // read on demand, snapshot is pre-loaded
prices.OnUpdate(func(m map[string]string) { /* tick */ })
```

Available: `WatchPrices`, `WatchOperations`, `WatchBalances`, `WatchObject`,
`WatchObjects`, `WatchAggregation`, `WatchExchangeState`, `WatchFills`,
`WatchFunding`, `WatchCandles`, `WatchTrades`, `WatchTwap`.

## Trading

```go
ex, _ := client.EnsurePerpsExchange(ctx, arca.CreatePerpsExchangeOptions{Ref: "/traders/t1/exchange"}).Wait(ctx)

nonce, _ := client.Nonce(ctx, "/op/order/btc")
order := client.PlaceOrder(ctx, arca.PlaceOrderOptions{
	Path: nonce.Path, ObjectID: ex.Object.ID,
	Coin: "hl:BTC", Side: arca.Buy, OrderType: "MARKET", Size: "0.01",
})
if _, err := order.Wait(ctx); err != nil { /* placement failed */ }
fill, _ := order.Filled(ctx)

// Pure fee/margin/liquidation preview (no network):
bd := arca.ComputeOrderBreakdown(arca.OrderBreakdownOptions{
	Amount: "200", AmountType: "spend", Leverage: 10,
	FeeRate: "0.00045", Price: "65000", Side: arca.Buy, SzDecimals: 5,
})
fmt.Println(bd.Tokens, bd.MarginRequired, bd.EstimatedFee)
```

## Admin

`arca.NewAdmin` is a separate, realm-less client for builder operations
(orgs, realms, API keys, members, invitations, scoped-token minting).

```go
admin := arca.NewAdmin(arca.AdminConfig{Token: builderJWT})
key, _ := admin.CreateApiKey(ctx, arca.CreateApiKeyOptions{
	Name: "ci", RealmID: "rlm_...", Permissions: arca.PermissionRead,
})
```

## Custody & recovery keys

```go
status, _ := client.GetCustodyStatus(ctx)
tx, _ := arca.PrepareWithdraw(status.ContractAddress, status.ChainID, boundaryID) // unsigned EVM tx

key, _ := arca.GenerateRecoveryKey() // 12-word mnemonic + EIP-55 address (client-side)
client.RegisterRecoveryKey(ctx, arca.RegisterRecoveryKeyOptions{BoundaryID: "b0", WalletAddress: key.Address})
```

## Conventions

- **Money** values are decimal strings (e.g. `"50"`, `"0.01"`).
- **Coin** ids are canonical `{exchange}:{id}` (`"hl:BTC"`, `"hl:1:TSLA"`) —
  case-sensitive, never bare symbols.
- **Market-data** timestamps are Unix epoch milliseconds; all other timestamps
  are RFC3339 UTC strings.

## Not yet ported

The following TypeScript surfaces are intentionally deferred to a follow-up and
are tracked in [llms.txt](./llms.txt): the merged equity/PnL/candle **chart
streams** (`watchEquityChart`, `watchPnlChart`, `watchCandleChart`) and the
derived `watchMaxOrderSize` stream. The underlying REST endpoints
(`GetEquityHistory`, `GetPnlHistory`, `GetCandles`) and the shared `ladder`
helpers are available today.

## Development

```bash
GOWORK=off go test ./...
GOWORK=off go vet ./...
```
