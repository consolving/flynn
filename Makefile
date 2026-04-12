# Portable readlink -f (works on macOS and GNU/Linux)
_readlink_f = $(shell cd $(1) && pwd -P 2>/dev/null)
GO_ENV = GOROOT=$(call _readlink_f,build/_go)

build:
	script/build-flynn

bootstrap-build:
	script/bootstrap-build

release:
	script/build-flynn --git-version

clean:
	script/clean-flynn

test: test-unit test-integration

# test-unit requires a prior 'make build' (uses GOROOT from build output)
test-unit: build
	env $(GO_ENV) PATH=${PWD}/build/bin:${PATH} go test -race -cover ./...

test-unit-root: test-unit
	sudo -E env $(GO_ENV) PATH=${PWD}/build/bin:${PATH} go test -race -cover ./host/volume/...

# test-unit-standalone runs pure Go tests without requiring 'make build'.
# Uses the system Go toolchain directly. Suitable for CI where Go is installed
# via the Dockerfile rather than extracted from a build image.
#
# These packages have no external service dependencies (no PostgreSQL, discoverd,
# Redis, ZFS, etc.) and can run in any Linux environment with Go installed.
TEST_PACKAGES_STANDALONE = \
	./pkg/attempt/... \
	./pkg/cors/... \
	./pkg/ipallocator/... \
	./pkg/lru/... \
	./pkg/mauth/compare/... \
	./pkg/mux/... \
	./pkg/pinned/... \
	./pkg/rpcplus/... \
	./pkg/signal/... \
	./pkg/stream/... \
	./pkg/syslog/rfc5424/... \
	./pkg/sirenia/state/... \
	./flannel/pkg/ip/... \
	./flannel/subnet/... \
	./host/resource/... \
	./controller/scheduler/... \
	./discoverd/client/... \
	./discoverd/health/... \
	./logaggregator/buffer/... \
	./logaggregator/snapshot/... \
	./router/proxyproto/...
# Excluded from standalone tests:
#   ./pkg/lockedfile/... — imports internal/testenv (Go stdlib internal, not allowed)
#   ./pkg/term/...       — requires /dev/tty (not available in CI containers)

test-unit-standalone:
	go test -mod=vendor -race -cover $(TEST_PACKAGES_STANDALONE)

test-integration: build
	script/run-integration-tests

.PHONY: build bootstrap-build release clean test test-unit test-unit-root test-unit-standalone test-integration
