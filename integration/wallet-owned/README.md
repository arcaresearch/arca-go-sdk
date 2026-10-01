# Owned wallet SDK integration

This nested test module isolates the fixture's EIP-712 dependency from SDK users.
It uses the candidate SDK by a test-only relative replace. No production endpoint,
keys or assets are accepted. Owner signatures use public disposable fixture keys.

Start an isolated Spanner emulator at 127.0.0.1:19010, with REST at :19020. From
`backend/services/platform-go` run:

```sh
SPANNER_EMULATOR_HOST=127.0.0.1:19010 V9_WALLET_OWNED_SERVE=1 \
V9_ACTION_HARNESS_FILE=/tmp/arca-owned-sdk-harness.json \
go test -tags integration ./internal/integration -run '^TestV9ActionRelayRoundTrip$' -count=1 -timeout=22m -v
```

After `ACTION_HARNESS_READY`, from this directory run:

```sh
V9_OWNED_SDK_HARNESS=/tmp/arca-owned-sdk-harness.json go test -count=1 -v
```

The harness runs the ordinary chain-head stream and platform API. The SDK client
uses actions, deposit links, account reads and wallet SSE. Only mint/stop are local
fixture controls. Successful completion stops the harness. No polling worker or
builder execution relay participates. The parent SDK's normal test command skips
this nested module. To verify a published artifact later, remove the test-only
replace in a temporary modfile and pin the exact published SDK version there.
