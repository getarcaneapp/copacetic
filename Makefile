################################################################################
# Library-only fork of Copacetic used by Arcane.                               #
################################################################################

INFOMARK = $(shell printf "\033[34;1m=>\033[0m")

.PHONY: build
build:
	$(info $(INFOMARK) Building all packages ...)
	go build ./...

.PHONY: lint
lint:
	$(info $(INFOMARK) Linting go code ...)
	golangci-lint run -v ./...

.PHONY: format
format:
	$(info $(INFOMARK) Formatting all go files with gofumpt ...)
	gofumpt -l -w .

# GIT_CONFIG_GLOBAL keeps a user's commit-signing config out of the chisel
# fixtures, which create throwaway git commits.
.PHONY: test
test:
	$(info $(INFOMARK) Running unit tests ...)
	GIT_CONFIG_GLOBAL=/dev/null go test ./... -coverpkg=./... $(CODECOV_OPTS)

.PHONY: setup
setup:
	$(info $(INFOMARK) Installing lint and format tooling ...)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.10.1
	go install mvdan.cc/gofumpt@v0.9.2
