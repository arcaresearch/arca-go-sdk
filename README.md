# Arca Go SDK

`github.com/arcaresearch/arca-go-sdk` — a Go SDK for the Arca platform
(accounts, payments, perpetuals trading, real-time streaming, and audit
trails). It is a hand-written port of the [TypeScript SDK](../typescript) with
idiomatic Go ergonomics: `context.Context` on every call, typed errors usable
with `errors.As`, generic operation handles, and channel/callback watch
streams.

## Install

```bash
go get github.com/arcaresearch/arca-go-sdk/v2@latest
```

```go
import arca "github.com/arcaresearch/arca-go-sdk/v2"
```

Requires Go 1.23+. It's a public module published from
[`github.com/arcaresearch/arca-go-sdk`](https://github.com/arcaresearch/arca-go-sdk),
so no `GOPRIVATE` or auth is needed. Pin a specific release with
`@vX.Y.Z` (e.g. `@v0.1.1`); `v0.1.0` is retracted (see [RELEASING.md](./RELEASING.md)).

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"

	arca "github.com/arcaresearch/arca-go-sdk/v2"
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
prices, _ := client.WatchPrices(ctx, &arca.WatchPricesOptions{Coins: []string{"hl:0:BTC"}})
defer prices.Close()
px, _ := prices.Get("hl:0:BTC") // read on demand, snapshot is pre-loaded
prices.OnUpdate(func(m map[string]string) { /* tick */ })
```

Available: `WatchPrices`, `WatchOperations`, `WatchBalances`, `WatchObject`,
`WatchObjects`, `WatchAggregation`, `WatchExchangeState`, `WatchFills`,
`WatchRealmFills`, `WatchRealmExchange`, `WatchFunding`, `WatchCandles`,
`WatchTrades`, `WatchTwap`.

The connection is rotated before the infrastructure's maximum socket lifetime
severs it: a replacement is warmed alongside the live one and only takes over
once the server confirms its subscriptions are live, so delivery never pauses
and no reconnect is surfaced. Set `Config.ConnectionLifetime` to override the
50-minute default, or to a pointer to `0` to disable rotation.

## Trading

```go
ex, _ := client.EnsurePerpsExchange(ctx, arca.CreatePerpsExchangeOptions{Ref: "/traders/t1/exchange"}).Wait(ctx)

nonce, _ := client.Nonce(ctx, "/op/order/btc")
order := client.PlaceOrder(ctx, arca.PlaceOrderOptions{
	Path: nonce.Path, ObjectID: ex.Object.ID,
	Coin: "hl:0:BTC", Side: arca.Buy, OrderType: "MARKET", Size: "0.01",
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
- **Coin** ids are canonical `{exchange}:{id}` (`"hl:0:BTC"`, `"hl:1:TSLA"`) —
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

## License

Licensed under the [PolyForm Shield License 1.0.0](./LICENSE). You may use,
modify, and redistribute this SDK for any purpose **except** building a product
or service that competes with Arca. See the LICENSE file for the full terms.

## Account-based execution confirmation

Use `PlaceOrder(...).Confirmed(ctx)` with a deadline when presenting an execution
result. MARKET/IOC/FOK waits for terminal order evidence; intentional resting
limits wait only for placement. `Submitted` is the HTTP acknowledgement and
`Wait` is operation settlement, neither alone guarantees a fill. Confirmation
never replays a trade. Keep its operation/path identity and use
`GetOperationOrder` or `ReconcileOrderKey` to recover unavailable outcomes.
Terminal partial fills retain cancelled remainder disposition and exact filled
quantity; fee evidence includes `fillsComplete` when the adapter can certify it.

Mutations are single-send on network/gateway failures. SDK errors preserve
`ArcaError.StatusCode` and `Details`, including legacy envelopes. Read retries
remain bounded. `GetExchangeCapabilities` exposes authoritative account feature
support; consumers must not infer features from market prefixes or venue names.

The normative ownership and evolution policy lives in the Arca monorepo at
`documents/contracts/builder-sdk-golden-contract.md`.


### Terminal execution receipts

`OrderHandle.ExecutionReceipt(ctx)` returns terminal execution evidence immediately
when the HTTP operation already contains it; it does not wait for a WebSocket ACK,
order-history reads, or individual ledger fills. Nonterminal responses use scoped
execution updates and an acknowledged snapshot. `Confirmed` keeps its existing
response type and uses this receipt path for immediate orders.

The receipt includes original `RequestedSize`, executed `FilledSize`, known
`RemainingSize`, `FulfillmentState`, and `RemainingDisposition`. Unknown original
intent stays unknown. A terminal IOC may be only partially fulfilled. Venue
aggregate prices have `AveragePriceFinal=false`; do not present them as exact
final VWAP. `FillsComplete=false` is independent of terminal execution.

`GetOperationExecutionReceipt(ctx, objectID, operation)` recovers from the original
operation before requesting account-scoped history. It rejects a different account
and never submits a replacement. `Filled` retains its full-order return type and
performs a metadata read after execution; unavailable or stale details remain an
error. Use `ExecutionReceipt` for prompt confirmation.

## Operation wait recovery

`WaitForOperation` listens before acquiring its subscription. Startup and actual
stream gaps, reauthentication, or sparse operation notifications request a fresh,
correlated acknowledgement before reading the operation. A failed acknowledgement
or read gets at most three attempts per recovery; a healthy pending operation
stays on the stream without periodic reads. A terminal push can complete during
acknowledgement or snapshot recovery. Timeout stops the wait and preserves the
original operation identity; it never submits a replacement operation.

Terminal operations in the initial or buffered subscription snapshot resolve the wait before any HTTP read, including typed failed/expired results. Socket rotation requests fresh operation evidence on the replacement connection; its pong only establishes transport readiness. Stale snapshot request IDs and unrelated operation IDs cannot settle the wait.
Verified snapshot operations are shared with all live waiters, including results buffered while another caller refreshes the same root watch; each waiter still accepts only its original operation ID.
