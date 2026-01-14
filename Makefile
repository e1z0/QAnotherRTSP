-include .APP
BUILD_FILE := BUILD
BUILD := $(shell cat $(BUILD_FILE))
VERSION := $(shell cat VERSION)
VERSION_WIN := $(VERSION).0.$(BUILD)
VERSION_COMMA := $(shell echo $(VERSION_WIN) | tr . ,)
LINES := $(shell wc -l $(SRC)/*.go | grep total | awk '{print $$1}')
REL_LINUX_BIN := $(BINARY)-linux
REL_MACOS_BIN := $(BINARY)-mac
REL_WINDOWS_BIN := $(BINARY)-win.exe
LINUX_SKEL := resources/linux-skeleton
MACOS_SKEL := resources/macos-skeleton
REL_DIR := release
MAC_APP_DIR := $(REL_DIR)/$(APP).app
ARCH := $(shell uname -m)
UID ?= $(shell id -u)
GID ?= $(shell id -g)
WINDOCKERIMAGE := nulldevil/ffmpeg-win64-cross-go1.23-qt5.15-static:latest
WIN32DOCKERIMAGE := nulldevil/ffmpeg-win32-cross-go1.23-qt5.15-static:latest
OSXINTELDOCKER := nulldevil/ffmpeg-macos-cross-x86_64-sdk13.1-go1.24.3-qt5.15-dynamic:latest
OSXARMDOCKER := nulldevil/ffmpeg-macos-cross-arm64-sdk13.1-go1.24.3-qt5.15-dynamic:latest
LINUX64DOCKER := nulldevil/ffmpeg-linux64-go1.24-qt5.15-dynamic:latest
LINUXARM64DOCKER := nulldevil/ffmpeg-linux-arm64-go1.24-qt5.15-dynamic:latest
OS := $(shell uname -s)
RUN_ENV_FILE := .docker_env

.ONESHELL:
SHELL := /bin/bash

ifeq ($(OS),Darwin)
    PLATFORM_VAR := "darwin"
    QT5_PREFIX := $(shell brew --prefix qt@5)
    RCC := $(QT_PREFIX)/share/qt/libexec/rcc
    FFMPEG_PREFIX := $(shell brew --prefix ffmpeg@7)
    DBUS_PREFIX := $(shell brew --prefix dbus)
    MAC_DEPLOY_QT := $(QT5_PREFIX)/bin/macdeployqt
    all: build_mac
else ifeq ($(OS),Linux)
    PLATFORM_VAR := "linux"
    RCC := rcc
    all: docker_linux_build
else ifeq ($(OS),FreeBSD)
    PLATFORM_VAR := "freebsd"
else
    PLATFORM_VAR := "unknown"
endif

EXPORTS := PLATFORM_VAR QT5_PREFIX DBUS_PREFIX MAC_DEPLOY_QT OS APP REL_DIR BINARY REL_MACOS_BIN REL_LINUX_BIN REL_WINDOWS_BIN MAC_APP_DIR ARCH LINUX_SKEL MACOS_SKEL VERSION BUILD IDPREF BUNDLE_COPYRIGHT
export $(EXPORTS)

help: ## Shows help contents
	@echo "Available targets:"
	@grep -hE '^[[:alnum:]][[:alnum:]._/-]*:([^#]|#[^#])*##' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*##[[:space:]]*"} {printf "  \033[1m%-25s\033[0m %s\n", $$1, $$2}' | \
		sort

# ---------- Docker builder macro ----------
# Usage examples:
#   $(call BUILD_DOCKER,ffmpeg-linux-go1.25-qt6.8-dynamic.Dockerfile,conan-build:fedora41,linux/amd64)
#   $(call BUILD_DOCKER,ffmpeg-linux-go1.25-qt6.8-dynamic.Dockerfile,conan-build:fedora41-arm64,linux/arm64,--load)
#
# Args:
#   1 = Dockerfile name (relative to docker/)
#   2 = Image tag
#   3 = Platform (optional) e.g. linux/amd64 linux/arm64 linux/arm/v7 ...
#   4 = Extra buildx args (optional) e.g. --load, --push, --build-arg FOO=bar ...
define BUILD_DOCKER
        @PLATFORM="$(3)"; \
        EXTRA="$(4)"; \
        if [ -z "$$PLATFORM" ]; then \
                PLATFORM_ARG=""; \
        else \
                PLATFORM_ARG="--platform $$PLATFORM"; \
        fi; \
        if ! docker image inspect "$(2)" > /dev/null 2>&1; then \
                echo "Image not found, building..."; \
                if command -v docker-buildx >/dev/null 2>&1 || docker buildx version >/dev/null 2>&1; then \
                        docker buildx build $$PLATFORM_ARG -f docker/$(1) -t "$(2)" $$EXTRA docker/; \
                else \
                        docker build $$PLATFORM_ARG -f docker/$(1) -t "$(2)" docker/; \
                fi; \
        else \
                echo "Docker image already built, using it..."; \
        fi
endef

# ---------- Docker run macro ----------
# Args:
#   1 = GOOS           (e.g., linux)
#   2 = GOARCH         (e.g., amd64 or arm64)
#   3 = ARCH triplet   (e.g., x86_64 or aarch64)
#   4 = Docker image   (e.g., $(LINUX64DOCKER))
#   5 = Command
#   6 = Fuse enabled 1
#   7 = interactive 1
define RUN_DOCKER
        if [ -s $(RUN_ENV_FILE) ]; then ENVFILE="--env-file $(RUN_ENV_FILE)"; else ENVFILE=""; fi; \
        PLATFORM=""; \
        if [ "$(1)" = "linux" ]; then \
                case "$(2)" in \
                        arm64|aarch64) PLATFORM="--platform linux/arm64" ;; \
                        arm|armhf)     PLATFORM="--platform linux/arm/v7" ;; \
                        armv6)         PLATFORM="--platform linux/arm/v6" ;; \
                        ppc64le)       PLATFORM="--platform linux/ppc64le" ;; \
                        s390x)         PLATFORM="--platform linux/s390x" ;; \
                        riscv64)       PLATFORM="--platform linux/riscv64" ;; \
                        *)             PLATFORM="" ;; \
                esac; \
        fi; \
	docker run $$PLATFORM --rm --init -i --user $(UID):$(GID) \
		$(if $(7),-t,) \
		$(if $(6),--device /dev/fuse --cap-add SYS_ADMIN --security-opt apparmor:unconfined,) \
                $$ENVFILE \
		-v ${HOME}/go/pkg/mod:/go/pkg/mod \
		-e GOMODCACHE=/go/pkg/mod \
		-v ${HOME}/.cache/go-build:/.cache/go-build \
		-e GOOS=$(1) -e GOARCH=$(2) -e ARCH=$(3) \
		-e GOCACHE=/.cache/go-build \
		-v ${PWD}:/src -w /src \
		-e HOME=/tmp \
		$(4) \
                bash -c ' \
                        if [[ "$$GOOS" == windows ]]; then \
                                sed -e "s/@VERSION_COMMA@/$(VERSION_COMMA)/g" \
                                    -e "s/@VERSION_DOT@/$(VERSION_WIN)/g" \
                                    $(SRC)/resource.rc.in > $(SRC)/resource.rc; \
                                if [[ "$$GOARCH" == "386" ]]; then \
                                        RES=i686-w64-mingw32.static-windres; \
                                elif [[ "$$GOARCH" == "amd64" ]]; then \
                                        RES=x86_64-w64-mingw32.static-windres; \
                                else \
                                        echo "Unsupported GOARCH=$$GOARCH for Windows resource build" >&2; exit 1; \
                                fi; \
                                $$RES $(SRC)/resource.rc -O coff -o $(SRC)/resource.syso; \
                        fi; \
                        $(strip $(5)) \
                '
endef

# ---------- Go build macro ----------
# Args:
#   1 = DEBUG 1
#   2 = WINDOWS 1
#   3 = Binary name
define GO_BUILD
	go build -ldflags "-X main.version=${VERSION} \
	-X main.build=${BUILD} \
	-X main.lines=$(LINES) \
	$(if $(1),-X main.debugging=false,) \
	-s -w \
	$(if $(2),-H windowsgui,) \
	" \
	$(if $(2),--tags=windowsqtstatic,) \
	-o $(3) $(SRC)
endef


# ---------- Linux app Bundle macro ----------
# Args:
#   1 = Binary name
#   2 = Arch (x86_64, aarch64, armhf, i386)
define APP_BUNDLE
	if [ ! -f "$(1)" ]; then echo "File $(1) not found, skipping. You should run make docker_linux_build first"; exit 1; fi; \
	[ -d $(LINUX_SKEL)/appDir ] && rm -rf $(LINUX_SKEL)/appDir; \
	mkdir -p $(LINUX_SKEL)/appDir/usr/bin/; \
	cp -f "$(1)" "$(LINUX_SKEL)/appDir/usr/bin/"; \
	cp -f $(SRC)/icon.png "$(LINUX_SKEL)/$(APP).png"; \
	printf "%s\n" \
		"[Desktop Entry]" \
		"Name=$(APP)" \
		"Exec=$(1)" \
		"Icon=$(APP)" \
		"Type=Application" \
		"Categories=Utility;" \
		> "$(LINUX_SKEL)/$(APP).desktop"; \
	/linuxdeploy-${2}/linuxdeploy-${2}.AppImage \
		--appdir $(LINUX_SKEL)/appDir \
		--desktop-file $(LINUX_SKEL)/$(APP).desktop \
		--icon-file $(LINUX_SKEL)/$(APP).png \
		--executable $(LINUX_SKEL)/appDir/usr/bin/$(1) \
		--plugin qt \
		--output appimage
endef

unsupported:
	@echo "Unsupported local operating system..."

docker_win: ## Make a Windows x86_64 builder docker container
	$(call BUILD_DOCKER,win64-cross-go1.23-qt5.15-static.Dockerfile,$(WINDOCKERIMAGE),,--load)
docker_win32: ## Make a Windows x86 builder docker container
	$(call BUILD_DOCKER,win32-cross-go1.23-qt5.15-static.Dockerfile,$(WIN32DOCKERIMAGE),,--load)
docker_mactel: ## Make a MacOS Intel builder docker container
	$(call BUILD_DOCKER,macos-cross-x86_64-sdk13.1-go1.23-qt5.15-dynamic.Dockerfile,$(OSXINTELDOCKER),,--load)
docker_macarm: ## Make a MacOS Arm builder docker container
	$(call BUILD_DOCKER,macos-cross-arm64-sdk13.1-go1.23-qt5.15-dynamic.Dockerfile,$(OSXARMDOCKER),,--load)

docker_linux: ## Make a Linux x64 builder docker container
	$(call BUILD_DOCKER,linux-go1.24-qt5.15-dynamic.Dockerfile,$(LINUX64DOCKER),linux/amd64,--load)
docker_linux_arm64: ## Make a Linux arm64 builder docker container
	$(call BUILD_DOCKER,linux-go1.24-qt5.15-dynamic.Dockerfile,$(LINUXARM64DOCKER),linux/arm64,--load)

docker_mactel_clean:
	docker image rm $(OSXINTELDOCKER)
docker_macarm_clean:
	docker image rm $(OSXARMDOCKER)
docker_win_clean:
	docker image rm $(WINDOCKERIMAGE)
docker_linux_clean:
	docker image rm $(LINUX64DOCKER)

build_win: res ## Build windows x86_64 static binary (using Docker container)
	@if test -f $(REL_WINDOWS_BIN); then rm $(REL_WINDOWS_BIN); fi
	$(call RUN_DOCKER,windows,amd64,amd64,$(WINDOCKERIMAGE),$(call GO_BUILD,0,1,$(REL_WINDOWS_BIN)),,)
	@if test -f $(SRC)/resource.syso; then rm $(SRC)/resource.syso; fi
build_win32: res ## Build windows x86 static binary (using Docker container)
	@if test -f $(REL_WINDOWS_BIN); then rm $(REL_WINDOWS_BIN); fi
	$(call RUN_DOCKER,windows,386,386,$(WIN32DOCKERIMAGE),$(call GO_BUILD,0,1,$(REL_WINDOWS_BIN)),,)
	@if test -f $(SRC)/resource.syso; then rm $(SRC)/resource.syso; fi


# Make zip file for windows release
release_win: res build_win reldir ## Release build for windows (makes .zip release)
	@if ! test -f "$(REL_WINDOWS_BIN)"; then echo "Windows binaries not found (required for release target)."; exit 1; fi
	@if test -f "$(REL_DIR)/$(APP)-Win-x86_64.zip"; then rm -f "$(REL_DIR)/$(APP)-Win-x86_64.zip"; fi
	@if test -d "$(REL_DIR)/$(APP)"; then rm -rf "$(REL_DIR)/$(APP)"; fi
	mkdir -p "$(REL_DIR)/$(APP)"
	mv -f "$(REL_WINDOWS_BIN)" "$(REL_DIR)/$(APP)/$(REL_WINDOWS_BIN)"
	@# add readme if necessary
	@#cp -f README.md "$(REL_DIR)/$(APP)/"; \
	cd "$(REL_DIR)" && zip -r "$(APP)-Win-x86_64.zip" "$(APP)" && rm -rf "$(APP)"
release_win32: res build_win32 reldir ## Release build for windows 32 bit (makes .zip release)
	@if ! test -f "$(REL_WINDOWS_BIN)"; then echo "Windows binaries not found (required for release target)."; exit 1; fi
	@if test -f "$(REL_DIR)/$(APP)-Win-x86.zip"; then rm -f "$(REL_DIR)/$(APP)-Win-x86.zip"; fi
	@if test -d "$(REL_DIR)/$(APP)"; then rm -rf "$(REL_DIR)/$(APP)"; fi
	mkdir -p "$(REL_DIR)/$(APP)"
	mv -f "$(REL_WINDOWS_BIN)" "$(REL_DIR)/$(APP)/$(REL_WINDOWS_BIN)"
	@# add readme if necessary
	@#cp -f README.md "$(REL_DIR)/$(APP)/"; \
	cd "$(REL_DIR)" && zip -r "$(APP)-Win-x86.zip" "$(APP)" && rm -rf "$(APP)"

# Linux x64 (local build on linux host) should be latest debian trixie or compatible release
build_linux: ## Local build for Linux
	$(call GO_BUILD,,,$(REL_LINUX_BIN))

release_linux: reldir docker_linux_build ## Release build for Linux (creates .appImage bundle using Docker container)
	@if ! test -f "$(REL_LINUX_BIN)"; then echo "Linux binaries not found (required for release target)."; exit 1; fi
	@if test -d $(LINUX_SKEL)/appDir; then rm -rf $(LINUX_SKEL)/appDir; fi
	@if test -f "$(REL_DIR)/$(APP)-x86_64.AppImage"; then rm -f "$(REL_DIR)/$(APP)-x86_64.AppImage"; fi
	$(call RUN_DOCKER,linux,amd64,x86_64,$(LINUX64DOCKER),$(call APP_BUNDLE,$(REL_LINUX_BIN),x86_64),1,)
	if [ -f $(APP)-x86_64.AppImage ]; then \
		rm -rf $(LINUX_SKEL)/appDir; \
		mv $(APP)-x86_64.AppImage $(REL_DIR)/$(APP)-Linux-x86_64.AppImage; \
		echo "Linux target released to $(REL_DIR)/$(APP)-Linux-x86_64.AppImage"; \
	fi
	@if test -f $(REL_LINUX_BIN); then rm $(REL_LINUX_BIN); fi
release_linux_arm64: reldir docker_linux_build_arm64 ## Release build for Linux arm64 (creates .appImage bundle using Docker container)
	@if ! test -f "$(REL_LINUX_BIN)"; then echo "Linux binaries not found (required for release target)."; exit 1; fi
	@if test -d $(LINUX_SKEL)/appDir; then rm -rf $(LINUX_SKEL)/appDir; fi
	@if test -f "$(REL_DIR)/$(APP)-aarch64.AppImage"; then rm -f "$(REL_DIR)/$(APP)-aarch64.AppImage"; fi
	$(call RUN_DOCKER,linux,arm64,aarch64,$(LINUXARM64DOCKER),$(call APP_BUNDLE,$(REL_LINUX_BIN),aarch64),1,)
	if [ -f $(APP)-aarch64.AppImage ]; then \
		rm -rf $(LINUX_SKEL)/appDir; \
		mv $(APP)-aarch64.AppImage $(REL_DIR)/$(APP)-Linux-aarch64.AppImage; \
		echo "Linux target released to $(REL_DIR)/$(APP)-Linux-aarch64.AppImage"; \
	fi
	@if test -f $(REL_LINUX_BIN); then rm $(REL_LINUX_BIN); fi

build_mac: check-qt
	@if test -f $(SRC)/resource.syso; then rm $(SRC)/resource.syso; fi
	CGO_ENABLED=1 \
        CGO_CXXFLAGS="-std=c++17 -stdlib=libc++ -fPIC" \
	PATH="$(QT5_PREFIX)/bin:$$PATH" \
	LDFLAGS="-L$(QT5_PREFIX)/lib" \
	CPPFLAGS="-I$(QT5_PREFIX)/include" \
	PKG_CONFIG_PATH="$(QT5_PREFIX)/lib/pkgconfig:$(FFMPEG_PREFIX)/lib/pkgconfig" \
	go build -ldflags "-X main.version=${VERSION} \
	-X main.build=${BUILD} \
	-X main.lines=$(LINES) \
	-X main.debugging=true \
	-v -s -w" -o $(REL_MACOS_BIN) $(SRC)

docker_mactel_build: res ## Build target for MacOS Intel (using Docker container)
	@if test -f $(REL_MACOS_BIN); then rm $(REL_MACOS_BIN); fi
	$(call RUN_DOCKER,darwin,amd64,amd64,$(OSXINTELDOCKER),$(call GO_BUILD,,,$(REL_MACOS_BIN),,),,)

docker_macarm_build: res ## Build target for MacOS Arm64 (using Docker container)
	@if test -f $(REL_MACOS_BIN); then rm $(REL_MACOS_BIN); fi
	$(call RUN_DOCKER,darwin,arm64,arm64,$(OSXARMDOCKER),$(call GO_BUILD,,,$(REL_MACOS_BIN),,),,)



docker_linux_build: docker_linux ## Build Linux x86_64 target
	@if test -f $(SRC)/resource.syso; then rm $(SRC)/resource.syso; fi
	@if test -f $(REL_LINUX_BIN); then rm $(REL_LINUX_BIN); fi
	$(call RUN_DOCKER,linux,amd64,x86_64,$(LINUX64DOCKER),$(call GO_BUILD,,,$(REL_LINUX_BIN)),,)
docker_linux_build_arm64: docker_linux_arm64 ## build Linux x86 target
	@if test -f $(SRC)/resource.syso; then rm $(SRC)/resource.syso; fi
	@if test -f $(REL_LINUX_BIN); then rm $(REL_LINUX_BIN); fi
	$(call RUN_DOCKER,linux,arm64,aarch64,$(LINUXARM64DOCKER),$(call GO_BUILD,,,$(REL_LINUX_BIN)),,)

release_mac: reldir ## Release build (App Bundle) for MacOS Apple/Intel. Can run on MacoS or Linux (uses docker container) architecture - autodetected
ifeq ($(OS),Linux)
	@echo "Linux host detected -> running macOS bundle build inside Docker"
	@rm -f $(RUN_ENV_FILE)
	@ARCH_LINE="$$(file -b "$(REL_MACOS_BIN)" 2>/dev/null || true)"; \
	if [ -z "$$ARCH_LINE" ]; then echo "Error: $(REL_MACOS_BIN) not found" >&2; exit 1; fi; \
	if echo "$$ARCH_LINE" | grep -qE 'arm64|aarch64'; then \
		IMG="$(OSXINTELDOCKER)"; VAR1=arm64; VAR2=amd64; \
	elif echo "$$ARCH_LINE" | grep -qE 'x86_64|amd64'; then \
		IMG="$(OSXARMDOCKER)"; VAR1=amd64; VAR2=amd64; \
	else \
		echo "Error: Unsupported mac binary arch: $$ARCH_LINE" >&2; exit 1; \
	fi;
	@$(foreach v,$(EXPORTS),printf '%s=%s\n' '$(v)' '$(value $(v))' >> $(RUN_ENV_FILE);)
	$(call RUN_DOCKER,darwin,$$VAR1,$$VAR2,$$IMG,./scripts/build-appbundle,,)
	@if test -f $(RUN_ENV_FILE); then rm -f $(RUN_ENV_FILE); fi
	@if test -f $(REL_MACOS_BIN); then rm -f $(REL_MACOS_BIN); fi
	@if test -f macdeployqtfix.log; then rm -f macdeployqtfix.log; fi
else ifeq ($(OS),Darwin)
	@echo "macOS host detected -> running locally"
	./scripts/build-appbundle
else
	@echo "Error: Unsupported OS: $(OS)" >&2
	@exit 1
endif


.PHONY: build check-qt


# Enter Linux x86_64 docker
docker_linux_enter: ## Enter docker build environment for Linux x86_64
	$(call RUN_DOCKER,linux,amd64,x86_64,$(LINUX64DOCKER),bash,1,1)
# Enter Linux aarch64 docker
docker_linux_arm64_enter: ## Enter docker build environment for Linux Aarch64
	$(call RUN_DOCKER,linux,arm64,aarch64,$(LINUXARM64DOCKER),bash,1,1)

# Enter MacOS x86_64 docker
docker_mactel_enter: ## Enter docker build environment for MacOS Intel
	$(call RUN_DOCKER,darwin,amd64,amd64,$(OSXINTELDOCKER),bash,,1)

# Enter MacOS ARM64 docker
docker_macarm_enter: ## Enter docker build environment for MacOS ARM
	$(call RUN_DOCKER,darwin,arm64,arm64,$(OSXARMDOCKER),bash,,1)

# Enter Windows docker
docker_win_enter: ## Enter docker build environment for Windows
	$(call RUN_DOCKER,windows,amd64,amd64,$(WINDOCKERIMAGE),bash,,1)

check-qt:
	@if [ -z "$(QT5_PREFIX)" ]; then \
	  echo "Error: Qt5 not found."; \
	  echo "Searched: /usr/local/Cellar/qt@5 and /opt/homebrew/opt/qt@5"; \
	  echo "Try: brew install qt@5"; \
	  exit 1; \
	fi

reldir:
	@if [ ! -d $(REL_DIR) ]; then mkdir $(REL_DIR); fi

debug:
	GOTRACEBACK=all GODEBUG='schedtrace=1000,gctrace=1' ./$(REL_MACOS_BIN)

cleanup:
	~/go/bin/staticcheck -checks=U1000 $(SRC)

deps:
	brew install qt@5 golang dylibbundler
	go install github.com/mappu/miqt/cmd/miqt-rcc@latest

res:
	~/go/bin/miqt-rcc -RccBinary $(RCC) -Input $(SRC)/resources.qrc -OutputGo $(SRC)/resources.qrc.go

