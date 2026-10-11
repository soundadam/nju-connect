# Packaging, macOS development, and release-check targets.

.PHONY: package
package:
	@test -n "$(VERSION)" || { echo "VERSION is required (for example: make package VERSION=1.2.0)" >&2; exit 64; }
	./scripts/package.sh "$(VERSION)"

.PHONY: macos-preview
macos-preview: build
	NJU_CONNECT_HELPER="$(CURDIR)/$(BINARY)" swift run --package-path macos -Xswiftc -DUI_DESIGN_PREVIEW nju-connect-menu

.PHONY: macos-dev-build
macos-dev-build:
	./scripts/run_macos_development.zsh --build-only

.PHONY: macos-dev
macos-dev:
	./scripts/run_macos_development.zsh

.PHONY: release-check
release-check: fmt-check test vet
	@swift test --package-path macos
	@git diff --check
	@echo "release checks passed; no artifact was published"
