# Packaging, local install, and release-check targets.

.PHONY: cli-release
cli-release:
	@./scripts/package-cli-release.sh "$(VERSION)"

.PHONY: package-macos
package-macos:
	@test -n "$(VERSION)" || { echo "VERSION is required (for example: make package-macos VERSION=1.0.0)" >&2; exit 64; }
	./scripts/package_macos_release.zsh "$(VERSION)"

.PHONY: macos-preview
macos-preview: build
	NJU_CONNECT_HELPER="$(CURDIR)/$(BINARY)" NJU_CONNECT_UI_LANGUAGE="$(UI_LANGUAGE)" swift run --package-path macos -Xswiftc -DUI_DESIGN_PREVIEW nju-connect-menu

.PHONY: macos-dev-build
macos-dev-build:
	NJU_CONNECT_UI_LANGUAGE="$(UI_LANGUAGE)" ./scripts/run_macos_development.zsh --build-only

.PHONY: macos-dev
macos-dev:
	NJU_CONNECT_UI_LANGUAGE="$(UI_LANGUAGE)" ./scripts/run_macos_development.zsh

.PHONY: package-macos-local
package-macos-local:
	@test -n "$(VERSION)" || { echo "VERSION is required (for example: make package-macos-local VERSION=1.1.0-alpha.1)" >&2; exit 64; }
	./scripts/package_local_macos.zsh "$(VERSION)"

.PHONY: versioning-test
versioning-test:
	@./scripts/test_versioning.zsh

.PHONY: local-update
local-update:
	@CHANNEL="$(or $(CHANNEL),alpha)" \
	 VERSION="$(VERSION)" \
	 COMMIT_TAP="$(or $(COMMIT_TAP),1)" \
	 PUSH_TAP="$(or $(PUSH_TAP),0)" \
	 INSTALL_UPDATE="$(or $(INSTALL_UPDATE),1)" \
	 ./scripts/local_update.zsh

.PHONY: release-check
release-check: fmt-check versioning-test test vet
	@swift test --package-path macos
	@git diff --check
	@echo "release checks passed; no artifact was published"

.PHONY: release-preview
release-preview: release-check package-macos-local
