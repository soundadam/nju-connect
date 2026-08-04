GO ?= go
BINARY := bin/soundconnect
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
RESEARCH_ROOT := research
EASYCONNECT_UPSTREAM := $(RESEARCH_ROOT)/upstream/easyconnect
EASYCONNECT_WORK := $(RESEARCH_ROOT)/work/easyconnect
EASYCONNECT_LOCK := $(RESEARCH_ROOT)/lock.json
PACKAGE ?= $(firstword $(wildcard $(EASYCONNECT_UPSTREAM)/package/*.deb))
EASYCONNECT_DEPENDENCY ?= $(firstword $(wildcard $(EASYCONNECT_UPSTREAM)/dependencies/*.deb))
BENCHTIME ?= 250ms
BENCHCOUNT ?= 5
LEAKCOUNT ?= 10
VERSION ?=
UI_LANGUAGE ?= en

.PHONY: build
build:
	mkdir -p $(dir $(BINARY))
	$(GO) build -o $(BINARY) ./cmd/soundconnect

.PHONY: cli-release
cli-release:
	@./scripts/package-cli-release.sh "$(VERSION)"

.PHONY: build-platforms
build-platforms:
	@set -eu; \
	for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -o $(BINARY)-$$os-$$arch ./cmd/soundconnect; \
	done

.PHONY: test
test:
	$(GO) test ./...

.PHONY: test-race
test-race:
	$(GO) test -race ./...

.PHONY: bench
bench:
	$(GO) test ./internal/runtime ./internal/nativeapp -run '^$$' -bench . -benchmem -benchtime=$(BENCHTIME) -count=$(BENCHCOUNT)

.PHONY: leak-check
leak-check:
	$(GO) test ./internal/runtime -run '^Test(NativeSessionOwnsAndJoinsCompleteRuntime|CohortJoinsPeerBeforeOpeningNextGeneration|OwnerWatchdogRenewsOnceAndJoinsAllWorkers|UserspaceRejectsInvalidInboundAndCloseUnblocksOutbound)$$' -count=$(LEAKCOUNT)

.PHONY: fmt
fmt:
	gofmt -w $$(find cmd internal -type f -name '*.go' -print)

.PHONY: fmt-check
fmt-check:
	@test -z "$$(gofmt -l $$(find cmd internal -type f -name '*.go' -print))"

.PHONY: check
check: fmt-check test test-race leak-check

.PHONY: package-macos
package-macos:
	@test -n "$(VERSION)" || { echo "VERSION is required (for example: make package-macos VERSION=0.1.0)" >&2; exit 64; }
	./scripts/package_macos_release.zsh "$(VERSION)"

.PHONY: macos-preview
macos-preview: build
	SOUNDCONNECT_HELPER="$(CURDIR)/$(BINARY)" SOUNDCONNECT_UI_LANGUAGE="$(UI_LANGUAGE)" swift run --package-path macos -Xswiftc -DUI_DESIGN_PREVIEW soundconnect-menu

.PHONY: package-macos-local
package-macos-local:
	@test -n "$(VERSION)" || { echo "VERSION is required (for example: make package-macos-local VERSION=0.1.0)" >&2; exit 64; }
	./scripts/package_local_macos.zsh "$(VERSION)"

.PHONY: speedtest-component
speedtest-component:
	@./scripts/build_speedtest_component.sh

.PHONY: research-init
research-init:
	install -d -m 0700 \
		$(EASYCONNECT_UPSTREAM)/package \
		$(EASYCONNECT_UPSTREAM)/dependencies \
		$(EASYCONNECT_WORK)/rootfs \
		$(EASYCONNECT_WORK)/dependencies \
		$(EASYCONNECT_WORK)/runtime \
		$(EASYCONNECT_WORK)/snapshots \
		$(EASYCONNECT_WORK)/reports

.PHONY: easyconnect-inspect
easyconnect-inspect:
	@./dev/easyconnect/inspect "$(PACKAGE)"

.PHONY: easyconnect-import
easyconnect-import: research-init
	@./dev/easyconnect/import "$(PACKAGE)" "$(CURDIR)/$(EASYCONNECT_UPSTREAM)/package" "$(CURDIR)/$(EASYCONNECT_LOCK)"

.PHONY: easyconnect-prepare
easyconnect-prepare:
	@./dev/easyconnect/prepare \
		"$(PACKAGE)" \
		"$(CURDIR)/$(EASYCONNECT_WORK)/rootfs" \
		"$(EASYCONNECT_DEPENDENCY)" \
		"$(CURDIR)/$(EASYCONNECT_WORK)/dependencies" \
		"$(CURDIR)/$(EASYCONNECT_LOCK)"

.PHONY: easyconnect-probe
easyconnect-probe:
	@./dev/easyconnect/probe \
		"$(CURDIR)/$(EASYCONNECT_WORK)/rootfs" \
		"$(CURDIR)/$(EASYCONNECT_WORK)/dependencies" \
		"$(CURDIR)/$(EASYCONNECT_WORK)/runtime" \
		ui

.PHONY: easyconnect-agent
easyconnect-agent:
	@./dev/easyconnect/probe \
		"$(CURDIR)/$(EASYCONNECT_WORK)/rootfs" \
		"$(CURDIR)/$(EASYCONNECT_WORK)/dependencies" \
		"$(CURDIR)/$(EASYCONNECT_WORK)/runtime" \
		agent

.PHONY: easyconnect-agent-check
easyconnect-agent-check:
	@./dev/easyconnect/control-check 54530

.PHONY: easyconnect-stop
easyconnect-stop:
	@./dev/easyconnect/stop "$(CURDIR)/$(EASYCONNECT_WORK)/runtime"

.PHONY: clean
clean:
	$(GO) clean
	rm -f $(BINARY)
	@echo "local .config/ state is preserved"
