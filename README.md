# getarcane.dev/copacetic

A library-only fork of [Project Copacetic](https://github.com/project-copacetic/copacetic),
used by [Arcane](https://github.com/getarcaneapp/arcane) to patch OS and library
vulnerabilities in container images with BuildKit.

```go
import copa "getarcane.dev/copacetic"

err := copa.Patch(ctx, &copa.Options{
	Image:      "docker.io/library/nginx:1.25",
	Report:     "report.json", // Trivy JSON report
	PatchedTag: "1.25-patched",
	Timeout:    10 * time.Minute,
})
```

`getarcane.dev/copacetic` is the only importable package: `Patch`, `Options`,
`ErrNoUpdatesFound`, and `ResolvePatchedImageName`. The engine lives under
`internal/`.

## How it differs from upstream

The patching engine is upstream's. The fork removes what a library consumer
does not need, so importing it pulls in far fewer modules:

| Removed | Replacement |
|---|---|
| `copa` CLI (`main.go`, `pkg/cmd`, cobra, viper) | none: call `copa.Patch` |
| Terminal UI (`pkg/tui`, lipgloss, progrock) | logrus messages and BuildKit's `progressui` |
| `github.com/docker/buildx` (`imagetools`) | go-containerregistry builds and pushes the multi-platform index (`internal/patch/manifest.go`) |
| `github.com/aquasecurity/trivy` (report types) | local structs matching Trivy's JSON format (`internal/report/trivytypes.go`) |
| `k8s.io/apimachinery` (`sets`) | a plain map |
| BuildKit frontend, bulk, generate, helm, integration and e2e suites, docs site | none |
| 18 public `pkg/...` packages | one root package; the rest moved to `internal/` |

Trivy is still the scanner: Copa reads its JSON report as before.

## Development

```sh
make build   # go build ./...
make test    # unit tests
make lint
```

## Syncing with upstream

```sh
git remote add upstream https://github.com/project-copacetic/copacetic.git
git fetch upstream --tags
git rebase <upstream-tag>   # replay the fork's commits; resolve conflicts in removed paths by deleting them
```

## License

Apache 2.0, as upstream. See [LICENSE](LICENSE).
