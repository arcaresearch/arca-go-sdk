# Releasing the Go SDK

The Go SDK is developed here in the monorepo at `sdk/go`, but its module path is
`github.com/arcaresearch/arca-go-sdk` and it is **consumed from a dedicated
public repo**: <https://github.com/arcaresearch/arca-go-sdk>. This mirrors the
TypeScript and Swift SDKs (developed here, published to `arca-typescript-sdk` /
`arca-swift-sdk`).

Why a separate public repo instead of consuming `sdk/go` directly:

- Go requires the import path to match a real VCS root. `github.com/arcaresearch/arca-go-sdk`
  resolves to the public mirror's root, so consumers just run
  `go get github.com/arcaresearch/arca-go-sdk@vX.Y.Z`.

## Consuming the SDK (for builders)

```bash
go get github.com/arcaresearch/arca-go-sdk@latest
```

```go
import arca "github.com/arcaresearch/arca-go-sdk"
```

It's a public module, so no `GOPRIVATE` or auth is needed. It's licensed under
the [PolyForm Shield License 1.0.0](./LICENSE) (source-available, anti-compete).

## Cutting a release (maintainers)

Releases use the same tag-driven flow as the other SDKs. From an up-to-date
`main`:

```bash
make publish-go-sdk-patch   # 0.1.1 -> 0.1.2
make publish-go-sdk-minor   # 0.1.1 -> 0.2.0
make publish-go-sdk-major   # 0.1.1 -> 1.0.0
```

This computes the next version from the latest `go-sdk/v*` monorepo tag (or the
`0.1.1` bootstrap floor if none exists), then pushes a `go-sdk/vX.Y.Z` tag. The
[`.github/workflows/sync-sdks.yml`](../../.github/workflows/sync-sdks.yml)
workflow does a history-preserving `git subtree split --prefix=sdk/go`,
force-pushes it to the `arca-go-sdk` mirror's `main`, and forwards the tag as
`vX.Y.Z` so `go get` resolves it. `make ship` releases Swift + TS + Go together.

### One-time prerequisite

The sync workflow authenticates with the `SDK_SYNC_PAT` repository secret — a
fine-grained PAT with **Contents: read and write**. It must be scoped to
`arca-go-sdk` in addition to `arca-swift-sdk` and `arca-typescript-sdk`. If the
Go leg of the sync workflow fails with an auth error, the PAT is missing that
repo.

### Manual fallback

`sdk/go/scripts/publish.sh [vX.Y.Z]` does the same subtree split + push + tag
directly from your machine using your own git push credentials (no PAT needed).
Use it for out-of-band publishes or if CI is unavailable.

## Versioning notes

- `v0.1.0` was published under the MIT license during bootstrap and is
  **retracted** in `go.mod`; it remains cached on the public Go module proxy.
- `v0.1.1` is the first PolyForm Shield release and the current floor. Follow
  semver for subsequent releases.
