# Local preview and release preparation targets.
# Public signing, notarization, upload, and tap publication are disabled until a
# Developer ID Application identity and notary profile are intentionally added.

.PHONY: cli-release
cli-release:
	@./scripts/package-cli-release.sh "$(VERSION)"

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

.PHONY: speedtest-helper-build
speedtest-helper-build:
	@./scripts/build_speedtest_component.sh

.PHONY: release-check
release-check: fmt-check test vet
	@swift test --package-path macos
	@git diff --check
	@echo "release checks passed; no artifact was published"

.PHONY: release-preview
release-preview: release-check package-macos-local

.PHONY: release-sign release-publish release-tap
release-sign release-publish release-tap:
	@echo "disabled: Developer ID signing, notarization, and public release are not configured" >&2
	@echo "use make release-preview VERSION=X.Y.Z for a local-only build" >&2
	@exit 78

# Future production flow, intentionally inactive:
# release-sign:       sign nested executables and App with Developer ID Application
#                     then submit, staple, and verify notarization.
# release-publish:    upload immutable assets only after anonymous SHA-256 checks.
# release-tap:        render and audit the public Cask from downloaded Release assets.
