# Releasing the Go SDK

The Go SDK is developed here in the monorepo at `sdk/go`, but its module path is
`github.com/arcaresearch/arca-go-sdk` and it is **consumed from a dedicated
public repo**: <https://github.com/arcaresearch/arca-go-sdk>. This mirrors the
TypeScript SDK (developed here, published to its own `arca-typescript-sdk` repo
+ npm).

Why a separate public repo instead of consuming `sdk/go` directly:

- This monorepo is **private**; external builders (the SDK's audience) can't
  `go get` from it.
- Go requires the import path to match a real VCS root. `github.com/arcaresearch/arca-go-sdk`
  resolves to the public repo's root, so consumers just run
  `go get github.com/arcaresearch/arca-go-sdk@vX.Y.Z` with no auth.

## Consuming the SDK (for builders)

```bash
go get github.com/arcaresearch/arca-go-sdk@latest
```

```go
import arca "github.com/arcaresearch/arca-go-sdk"
```

## Cutting a release (maintainers)

The public repo is a generated mirror of `sdk/go`. To publish, run the release
script from an up-to-date `main`:

```bash
# sync main only (no new version):
./sdk/go/scripts/publish.sh

# sync main AND tag a release:
./sdk/go/scripts/publish.sh v0.2.0
```

The script does a history-preserving `git subtree split --prefix=sdk/go` and
force-pushes the result to the public repo's `main`, optionally creating the
`vX.Y.Z` tag. It uses your normal git push credentials — no stored secret.

Follow semver. Bump the version whenever the public API surface changes; the
`v0.x` series may include breaking changes between minors.

## Optional: automatic sync via CI

`.github/workflows/sync-go-sdk.yml` syncs the public repo automatically on any
push to `main` that touches `sdk/go/**`, and can tag a release via
**Run workflow** (`workflow_dispatch`) with a `version` input.

It is a **no-op until a credential is configured**, because GitHub
organization policy disables deploy keys on the public repo. To enable it:

1. Create a **fine-grained personal access token** (or a GitHub App token)
   scoped to `arcaresearch/arca-go-sdk` with **Contents: read and write**.
2. Add it to this repo as an Actions secret named `ARCA_GO_SDK_SYNC_TOKEN`
   (`gh secret set ARCA_GO_SDK_SYNC_TOKEN -R arcaresearch/arca`).

Until then, use the manual `publish.sh` flow above.
