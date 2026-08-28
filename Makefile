# DemucsStudio build & packaging.
#
#   make dev              live-reload development build
#   make linux            Linux binary (build/bin/DemucsStudio)
#   make windows          Windows .exe, cross-compiled from Linux
#   make darwin           macOS universal .app          (macOS host only)
#   make package          tar.gz + .deb + .rpm + .zip for Linux and Windows
#   make package-darwin   .dmg for macOS                (macOS host only)
#   make test             unit tests
#   make e2e              end-to-end tests (network + GPU/CPU heavy)
#
# Releases go through scripts/release-build.sh (Linux + Windows) and
# scripts/release-build-macos.sh (macOS); both stamp the version into wails.json
# and assert their artifacts exist. CI never calls this Makefile directly.
#
# Cross-compiling to Windows works because Wails' Windows backend uses the pure
# Go WebView2 loader, so CGO is not needed. The Linux build does need CGO and
# webkit2gtk, and the macOS build needs CGO plus the system frameworks, so
# neither can be produced anywhere but its own OS — hence `package` covers only
# Linux and Windows, and macOS gets its own target and its own CI job.

APP        := DemucsStudio
BIN        := demucs-studio
VERSION    ?= 0.1.0
LDFLAGS    := -s -w -X main.appVersion=$(VERSION)
DIST       := dist

# Ubuntu/Debian 24.04+ ship webkit2gtk 4.1; Wails needs the build tag for it.
LINUX_TAGS := webkit2_41

.PHONY: all dev bindings linux windows windows-installer darwin package \
        package-linux package-windows package-darwin deb test e2e clean doctor \
        fmt icons

all: linux windows

doctor:
	wails doctor

# Regenerate every icon artifact from build/appicon.png. Run after replacing
# that file; the generated files are committed, so a normal build needs nothing.
icons:
	python3 scripts/make-icons.py

dev:
	wails dev -tags $(LINUX_TAGS)

fmt:
	gofmt -w .
	cd frontend && npx prettier --write "src/**/*.{ts,css}" "index.html" 2>/dev/null || true

test:
	go test ./...

# Hits YouTube and runs real separations; see pipeline_test.go.
e2e:
	DEMUCS_STUDIO_E2E=1 go test -run TestPipeline -timeout 90m -v .

linux:
	wails build -tags $(LINUX_TAGS) -ldflags "$(LDFLAGS)" -o $(BIN)

# Binding generation compiles and runs the app, so it only works for the host
# platform. The Windows build therefore has to reuse bindings produced here,
# which is why it passes -skipbindings.
bindings:
	wails generate module -tags $(LINUX_TAGS)

windows: bindings
	CGO_ENABLED=0 wails build -platform windows/amd64 -skipbindings \
		-ldflags "$(LDFLAGS)" -o $(BIN).exe

# NSIS installer. Requires makensis (apt install nsis); falls back with a
# clear message rather than a cryptic error.
windows-installer:
	@command -v makensis >/dev/null 2>&1 || { \
		echo "makensis không có. Cài bằng: sudo apt install nsis"; exit 1; }
	CGO_ENABLED=0 wails build -platform windows/amd64 -skipbindings -nsis \
		-ldflags "$(LDFLAGS)" -o $(BIN).exe

# One universal binary rather than two per-arch builds: Wails lipos the amd64
# and arm64 slices together, so a single .dmg covers Intel and Apple Silicon.
# Bindings are generated natively here, so unlike `windows` this needs no
# -skipbindings.
darwin:
	wails build -platform darwin/universal -ldflags "$(LDFLAGS)" -o $(APP)

package: package-linux package-windows
	@echo
	@ls -lh $(DIST)

package-linux: linux
	@mkdir -p $(DIST) $(DIST)/stage-linux/$(BIN)
	cp build/bin/$(BIN) $(DIST)/stage-linux/$(BIN)/
	cp build/appicon.png $(DIST)/stage-linux/$(BIN)/
	cp README.md $(DIST)/stage-linux/$(BIN)/
	cp packaging/linux/run.sh $(DIST)/stage-linux/$(BIN)/
	chmod +x $(DIST)/stage-linux/$(BIN)/run.sh
	tar -czf $(DIST)/$(BIN)-$(VERSION)-linux-amd64.tar.gz \
		-C $(DIST)/stage-linux $(BIN)
	rm -rf $(DIST)/stage-linux
	$(MAKE) deb

# nfpm builds .deb and .rpm from one config.
deb:
	@command -v nfpm >/dev/null 2>&1 || { \
		echo "nfpm không có. Cài bằng: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest"; \
		exit 0; }
	@mkdir -p $(DIST)
	VERSION=$(VERSION) nfpm package -f packaging/linux/nfpm.yaml -p deb -t $(DIST)
	VERSION=$(VERSION) nfpm package -f packaging/linux/nfpm.yaml -p rpm -t $(DIST)

# hdiutil and ditto ship with macOS, so the .dmg needs no extra tooling.
# create-dmg would add a Homebrew dependency plus an AppleScript step to style
# the window, which is a common source of CI flakiness for no real gain here.
# ditto rather than cp -R: it is the only copy that reliably preserves bundle
# metadata and the symlinks inside frameworks.
package-darwin: darwin
	@mkdir -p $(DIST) $(DIST)/stage-mac
	# Wails takes the bundle directory name from wails.json, not from -o — that
	# flag only names the executable inside Contents/MacOS. So the build leaves
	# build/bin/demucs-studio.app even though $(APP) is DemucsStudio. Resolve
	# whatever was produced rather than assuming, then stage it under the display
	# name the .dmg and the docs use. Renaming the directory is safe because
	# CFBundleExecutable names the inner binary, which -o already set to $(APP).
	@bundle=$$(ls -d build/bin/*.app 2>/dev/null | head -1); \
	if [ -z "$$bundle" ]; then \
		echo "package-darwin: không có .app nào trong build/bin"; exit 1; \
	fi; \
	echo "ditto $$bundle $(DIST)/stage-mac/$(APP).app"; \
	ditto "$$bundle" "$(DIST)/stage-mac/$(APP).app"
	cp packaging/macos/README.md $(DIST)/stage-mac/
	# Apple Silicon refuses to launch an arm64 bundle carrying no signature at
	# all. An ad-hoc signature needs no certificate and is enough to start the
	# app; it does not satisfy Gatekeeper, so a user who downloads the .dmg
	# still has to clear the quarantine flag — packaging/macos/README.md says
	# how.
	codesign --force --deep --sign - $(DIST)/stage-mac/$(APP).app
	# The /Applications symlink is what makes the window a drag-to-install one.
	ln -sf /Applications $(DIST)/stage-mac/Applications
	hdiutil create -volname "$(APP) $(VERSION)" -srcfolder $(DIST)/stage-mac \
		-ov -format UDZO $(DIST)/$(BIN)-$(VERSION)-macos-universal.dmg
	rm -rf $(DIST)/stage-mac

package-windows: windows
	@mkdir -p $(DIST) $(DIST)/stage-win/$(APP)
	cp build/bin/$(BIN).exe $(DIST)/stage-win/$(APP)/
	cp packaging/windows/README.md $(DIST)/stage-win/$(APP)/
	cd $(DIST)/stage-win && zip -qr ../$(BIN)-$(VERSION)-windows-amd64.zip $(APP)
	rm -rf $(DIST)/stage-win

clean:
	rm -rf build/bin $(DIST) frontend/dist/assets frontend/dist/index.html
