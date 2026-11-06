# AGENTS.md

## Scope
These instructions apply to the entire repository. If a nested `AGENTS.md` is added later, the nearest file to the changed code takes precedence. Direct user or maintainer instructions take precedence over this file.

Keep this guidance practical: prefer the smallest focused change that satisfies the task, and avoid repo-wide rewrites unless explicitly requested.

## Project overview
This is a library-only fork of Copacetic (`copa`), used by Arcane to patch vulnerabilities in existing container images with BuildKit. It updates packages in images without requiring a full source rebuild, usually from a scanner report such as Trivy.

- Module: `getarcane.dev/copacetic` (upstream: `github.com/project-copacetic/copacetic`)
- Public API: the root package `copacetic.go` (`Patch`, `Options`, `ErrNoUpdatesFound`, `ResolvePatchedImageName`). Everything else is under `internal/`; keep it that way and expose new API only through the root package.
- There is no CLI, TUI, BuildKit frontend, docs site, or integration/e2e suite. Do not reintroduce them, or the dependencies they need (cobra, viper, buildx, trivy, testcontainers, k8s, lipgloss, progrock). See `README.md` for what replaced each one.

## Design and safety guidelines
- Preserve Copa's design tenets: patch existing images, work with the current container/scanner/package-manager ecosystem, enable remediation by non-image-authors, and keep features composable and narrowly scoped.
- Treat image patching as security-sensitive. Do not weaken validation, package selection, provenance, VEX generation, image reference handling, platform handling, or scanner-report parsing.
- Prefer integrating with existing scanners, OS package managers, language package managers, BuildKit, Docker, and Podman instead of adding unrelated build/scanning systems.
- Discuss or confirm large architectural changes, new major features, or broad behavior changes before implementing them.
- Never commit secrets or credentials. Do not disclose suspected vulnerabilities publicly; follow `SECURITY.md`.

## Repository map
- `internal/patch/`: core single-arch and multi-platform patch orchestration.
- `internal/buildkit/`: BuildKit client setup, drivers, and platform discovery helpers.
- `internal/pkgmgr/`: OS package manager adapters (`apk`, `dpkg`, `rpm`, `pacman`) and scripts/testdata.
- `internal/langmgr/`: language/library package patching managers.
- `internal/report/`: vulnerability report parsing and scanner plugin interface; Trivy support lives here, using the local report types in `trivytypes.go`.
- `internal/imageloader/`: Docker/Podman image loading integration.
- `internal/provenance/`: provenance detection/rebuild helpers.
- `internal/vex/`: VEX/OpenVEX output.
- `internal/types/`: shared options, errors, and versioned API types.
- `internal/common/`, `internal/utils/`: shared helpers and BuildKit progress display.
- `internal/patch/manifest.go`: builds and pushes the multi-platform index with go-containerregistry.

## Build, test, lint, and format
Use the existing Makefile targets when possible:

- Install local tooling: `make setup` (installs pinned `golangci-lint` and `gofumpt`).
- Build: `make build`.
- Run unit tests: `make test` (`go test ./... $(CODECOV_OPTS)`).
- Run targeted unit tests while iterating: `go test ./internal/<package>` or `go test ./internal/<package> -run <TestName>`.
- Lint: `make lint`.
- Format Go code: `make format`.

`make test` sets `GIT_CONFIG_GLOBAL=/dev/null` because the chisel tests create git commits and fail under a global `commit.gpgsign`. End-to-end patching is verified from Arcane.

## Go coding conventions
- Follow the Go version in `go.mod`.
- Use `gofumpt` formatting and keep imports clean; linting also enforces `gofmt`, `goimports`, and repository-specific `golangci-lint` rules.
- Keep error messages actionable and wrap underlying errors with context (`fmt.Errorf("...: %w", err)`) unless the surrounding code uses a different established style.
- Use `logrus` for logging where existing code does; avoid ad-hoc `fmt.Println` in library paths.
- Prefer small, testable functions and table-driven tests consistent with nearby code.
- Add or update representative tests and `testdata` for package manager behavior, report parsing, version selection, platform handling, and edge cases.
- Do not edit generated, vendored, or versioned documentation files unless the task specifically requires it.

## Domain-specific notes
- OS package changes must preserve distro-specific semantics for package names, installed/fixed versions, architectures, and package manager commands.
- Scanner/report changes must preserve the scanner plugin interface and avoid assuming Trivy-only fields unless the code path is explicitly Trivy-specific.
- Multi-platform changes must preserve manifest-list behavior, target-platform selection, and platform preservation for unaffected images.
- BuildKit and image-loader changes should keep Docker, Podman, remote BuildKit, and cancellation/timeout behavior in mind.
- Language/library patching is experimental. Respect the `PkgTypes`, `LibraryPatchLevel`, and `ToolchainPatchLevel` options. Update relevant language manager tests when changing this area.
- VEX/provenance changes should not overstate remediation status; only report what the patching flow has actually validated.

## Documentation and PR expectations
- Update `README.md` when changing the public API, options, outputs, or how the fork differs from upstream.
- Keep examples aligned with current command names and flags.
- Contributions are expected to pass `make test`, `make lint`, and `gofumpt` formatting.
- The project uses Angular-style commit messages for changelog automation and requires DCO signoff (`Signed-off-by`) on real commits.
