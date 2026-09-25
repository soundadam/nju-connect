# Build, test, format, and cleanup targets.

.PHONY: build
build:
	mkdir -p $(dir $(BINARY))
	$(GO) build -o $(BINARY) ./cmd/soundconnect
	@if [ "$$(uname -s)" = "Darwin" ]; then \
		swift build --package-path macos -c release --product soundconnect-atrust-oauth-helper; \
		cp "$$(swift build --package-path macos -c release --show-bin-path)/soundconnect-atrust-oauth-helper" "$(dir $(BINARY))"; \
	fi

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

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: bench
bench:
	$(GO) test ./internal/runtime ./internal/backend/easyconnect/session -run '^$$' -bench . -benchmem -benchtime=$(BENCHTIME) -count=$(BENCHCOUNT)

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
check: fmt-check test test-race vet leak-check

.PHONY: clean
clean:
	$(GO) clean
	rm -f $(BINARY)
	@echo "local .config/ state is preserved"
