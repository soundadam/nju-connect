GO ?= go
BINARY := bin/soundconnect
LOCAL_STATE := .config
RESEARCH_ROOT := research
EASYCONNECT_UPSTREAM := $(RESEARCH_ROOT)/upstream/easyconnect
EASYCONNECT_WORK := $(RESEARCH_ROOT)/work/easyconnect
EASYCONNECT_LOCK := $(RESEARCH_ROOT)/lock.json
PACKAGE ?= $(firstword $(wildcard $(EASYCONNECT_UPSTREAM)/package/*.deb))

.PHONY: build
build:
	mkdir -p $(dir $(BINARY))
	$(GO) build -o $(BINARY) ./cmd/soundconnect

.PHONY: test
test:
	$(GO) test ./...

.PHONY: test-race
test-race:
	$(GO) test -race ./...

.PHONY: fmt
fmt:
	gofmt -w $$(find cmd internal -type f -name '*.go' -print)

.PHONY: fmt-check
fmt-check:
	@test -z "$$(gofmt -l $$(find cmd internal -type f -name '*.go' -print))"

.PHONY: check
check: fmt-check test test-race

.PHONY: dev-init
dev-init:
	install -d -m 0700 $(LOCAL_STATE)

.PHONY: research-init
research-init:
	install -d -m 0700 \
		$(EASYCONNECT_UPSTREAM)/package \
		$(EASYCONNECT_WORK)/rootfs \
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
	@./dev/easyconnect/prepare "$(PACKAGE)" "$(CURDIR)/$(EASYCONNECT_WORK)/rootfs"

.PHONY: clean
clean:
	$(GO) clean
	rm -f $(BINARY)
	@echo "local .config/ state is preserved"
