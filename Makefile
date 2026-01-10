APP := AnotherRTSP
SRC := ./src
BUILD_FILE := BUILD
BUILD := $(shell cat $(BUILD_FILE))
VERSION := $(shell cat VERSION)
VERSION_WIN := $(VERSION).0.$(BUILD)
VERSION_COMMA := $(shell echo $(VERSION_WIN) | tr . ,)
LINES := $(shell wc -l $(SRC)/*.go | grep total | awk '{print $$1}')
BINARY := another-rtsp
REL_LINUX_BIN := $(BINARY)-linux
REL_MACOS_BIN := $(BINARY)-mac
REL_WINDOWS_BIN := $(BINARY)-win.exe
REL_DIR := release
MAC_APP_DIR := $(REL_DIR)/$(APP).app
ARCH := $(shell uname -m)
UID ?= $(shell id -u)
GID ?= $(shell id -g)
WINDOCKERIMAGE := nulldevil/qanotherrtsp-win64-cross-go1.23-qt5.15-static:latest
OSXINTELDOCKER := nulldevil/qanotherrtsp-macos-cross-x86_64-sdk13.1-go1.24.3-qt5.15-dynamic:latest
OSXARMDOCKER := nulldevil/qanotherrtsp-macos-cross-arm64-sdk13.1-go1.24.3-qt5.15-dynamic:latest
LINUX64DOCKER := nulldevil/qanotherrtsp-linux64-go1.24-qt5.15-dynamic:latest
LINUXARM64DOCKER := nulldevil/qanotherrtsp-linux-arm64-go1.24-qt5.15-dynamic:latest
OS := $(shell uname -s)
RUN_ENV_FILE := .docker_env

ifeq ($(OS),Darwin)
    PLATFORM_VAR := "darwin"
    QT5_PREFIX := $(shell brew --prefix qt@5)
    FFMPEG_PREFIX := $(shell brew --prefix ffmpeg@7)
    MAC_DEPLOY_QT := $(QT5_PREFIX)/bin/macdeployqt
else ifeq ($(OS),Linux)
    PLATFORM_VAR := "linux"
else ifeq ($(OS),FreeBSD)
    PLATFORM_VAR := "freebsd"
else
    PLATFORM_VAR := "unknown"
endif

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
        ./utils/linuxdeploy-${2}.AppImage \
                --appdir $(LINUX_SKEL)/appDir \
                --desktop-file $(LINUX_SKEL)/$(APP).desktop \
                --icon-file $(LINUX_SKEL)/$(APP).png \
                --executable $(LINUX_SKEL)/appDir/usr/bin/$(1) \
                --plugin qt \
                --output appimage
endef


# default build (my mac :D)
all: build_mac

docker_win: ## Make a Windows builder docker container
	$(call BUILD_DOCKER,win64-cross-go1.23-qt5.15-static.Dockerfile,$(WINDOCKERIMAGE))

docker_mactel: ## Make a MacOS Intel builder docker container
	$(call BUILD_DOCKER,macos-cross-x86_64-sdk13.1-go1.23-qt5.15-dynamic.Dockerfile,$(OSXINTELDOCKER))

docker_macarm: ## Make a MacOS Arm builder docker container
	$(call BUILD_DOCKER,macos-cross-arm64-sdk13.1-go1.23-qt5.15-dynamic.Dockerfile,$(OSXARMDOCKER))

docker_linux: ## Make a Linux x64 builder docker container
	$(call BUILD_DOCKER,linux64-go1.24-qt5.15-dynamic.Dockerfile,$(LINUX64DOCKER))
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

docker_build_win:
	$(call RUN_DOCKER,windows,amd64,amd64,$(WINDOCKERIMAGE),$(call GO_BUILD,,1,$(REL_WINDOWS_BIN)),,)
	@if [ -e $(SRC)/resource.syso ]; then \
		rm $(SRC)/resource.syso; \
	fi

# Make zip file for windows release
release_win: ## Release build for Windows x64 using docker
	@if [ -f $(REL_DIR)/$(APP)-Win-x64.zip ]; then \
	rm $(REL_DIR)/$(APP)-Win-x64.zip; \
        fi
	@if [ -f $(REL_WINDOWS_BIN) ]; then \
	mv $(REL_WINDOWS_BIN) ${REL_DIR}/$(APP).exe; \
	cd $(REL_DIR) && zip $(APP)-Win-x64.zip $(APP).exe && rm $(APP).exe && cd ..; \
	else \
	echo "Binary $(REL_WINDOWS_BIN) cannot be found, first run docker_build_win"; \
	fi

# Linux x64 (local build on linux host) should be latest debian trixie or compatible release
build_linux: ## Local build for Linux
	$(call GO_BUILD,,,$(REL_LINUX_BIN))

# Make appImage release for Linux
release_linux: ## Release build for Linux x86_64 (appBundle)
	$(call RUN_DOCKER,linux,amd64,x86_64,$(LINUX64DOCKER),$(call APP_BUNDLE,$(REL_LINUX_BIN),x86_64),1,)
	if [ -f $(APP)-x86_64.AppImage ]; then \
		rm -rf resources/linux-skeleton/appDir; \
		mv $(APP)-x86_64.AppImage release/$(APP)-Linux-x86_64.AppImage; \
		echo "Linux target released to release/$(APP)-Linux-x86_64.AppImage"; \
	fi
release_linux_arm64: ## Release build for Linux arm64 (appBundle)
	$(call RUN_DOCKER,linux,arm64,aarch64,$(LINUXARM64DOCKER),$(call APP_BUNDLE,$(REL_LINUX_BIN),aarch64),1,)
	if [ -f $(APP)-aarch64.AppImage ]; then \
		rm -rf resources/linux-skeleton/appDir; \
		mv $(APP)-aarch64.AppImage release/$(APP)-Linux-aarch64.AppImage; \
		echo "Linux target released to release/$(APP)-Linux-aarch64.AppImage"; \
	fi
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

docker_build_mactel: ## Build project for MacOS Intel
	$(call RUN_DOCKER,darwin,amd64,amd64,$(OSXINTELDOCKER),./scripts/dockerbuild,,)

docker_build_macarm: ## Build project for MacOS arm
	$(call RUN_DOCKER,darwin,arm64,arm64,$(OSXARMDOCKER),./scripts/dockerbuild,,)

docker_build_linux: docker_linux
	@if test -f $(SRC)/resource.syso; then rm $(SRC)/resource.syso; fi
	$(call RUN_DOCKER,linux,amd64,x86_64,$(LINUX64DOCKER),$(call GO_BUILD,,,$(REL_LINUX_BIN)),,)
docker_build_linux_arm64: docker_linux_arm64
	@if test -f $(SRC)/resource.syso; then rm $(SRC)/resource.syso; fi
	$(call RUN_DOCKER,linux,arm64,aarch64,$(LINUXARM64DOCKER),$(call GO_BUILD,,,$(REL_LINUX_BIN)),,)

release_mac: ## Release build for MacOS Apple Silicon/Intel (local mac machine only)
	[ -d $(MAC_APP_DIR) ] && rm -rf $(MAC_APP_DIR) || true
	[ -f $(REL_DIR)/$(APP)-$(ARCH)-Mac.zip ] && rm $(REL_DIR)/$(APP)-$(ARCH)-Mac.zip || true
	[ -f $(REL_DIR)/$(APP)-$(ARCH).dmg ] && rm $(REL_DIR)/$(APP)-$(ARCH).dmg || true
	cp -r resources/macos-skeleton $(MAC_APP_DIR)
	mkdir $(MAC_APP_DIR)/Contents/{MacOS,Frameworks,libs}
	cp $(REL_MACOS_BIN) $(MAC_APP_DIR)/Contents/MacOS/$(BINARY)
	# copy all necessary ffmpeg folders
	chmod +x $(MAC_APP_DIR)/Contents/MacOS/$(BINARY)
	dylibbundler -od -b -x ./$(MAC_APP_DIR)/Contents/MacOS/$(BINARY) -d ./$(MAC_APP_DIR)/Contents/libs/
	$(MAC_DEPLOY_QT) $(MAC_APP_DIR) -verbose=1 -always-overwrite -executable=$(MAC_APP_DIR)/Contents/MacOS/$(BINARY)
	@# hide app from dock 
	@#/usr/libexec/PlistBuddy -c "Add :LSUIElement bool true" "$(MAC_APP_DIR)/Contents/Info.plist"
	@# add copyright
	@/usr/libexec/PlistBuddy -c "Add :NSHumanReadableCopyright string © 2025 e1z0. All rights reserved." "$(MAC_APP_DIR)/Contents/Info.plist"
	@# add version information
	/usr/libexec/PlistBuddy -c "Add :CFBundleShortVersionString string $(VERSION)" "$(MAC_APP_DIR)/Contents/Info.plist"
	@# add build information
	/usr/libexec/PlistBuddy -c "Add :CFBundleVersion string $(BUILD)" "$(MAC_APP_DIR)/Contents/Info.plist"
	codesign --force --deep --sign - $(MAC_APP_DIR)
	touch $(MAC_APP_DIR)
	hdiutil create $(REL_DIR)/$(APP)-$(ARCH).dmg -volname "AnotherRTSP" -fs HFS+ -srcfolder $(MAC_APP_DIR)
	#@bash -c 'pushd $(REL_DIR) > /dev/null; zip -r $(APP)-$(ARCH)-Mac.zip $(APP).app; popd > /dev/null'
	#@echo "Output: $(MAC_APP_DIR) and $(APP)-$(ARCH)-Mac.zip" 
.PHONY: build check-qt

# Enter MacOS x86_64 docker
docker_mactel_enter: ## Enter docker build environment for MacOS Intel
	$(call RUN_DOCKER,darwin,amd64,amd64,$(OSXINTELDOCKER),bash,,1)

# Enter MacOS ARM64 docker
docker_macarm_enter: ## Enter docker build environment for MacOS ARM
	$(call RUN_DOCKER,darwin,arm64,arm64,$(OSXARMDOCKER),bash,,1)

# Enter Windows docker
docker_win_enter: ## Enter docker build environment for Windows
	$(call RUN_DOCKER,windows,amd64,amd64,$(WINDOCKERIMAGE),bash,,1)

# Enter Linux docker
docker_linux_enter: docker_linux
	$(call RUN_DOCKER,linux,amd64,x86_64,$(LINUX64DOCKER),bash,1,1)

check-qt:
	@if [ -z "$(QT5_PREFIX)" ]; then \
	  echo "Error: Qt5 not found."; \
	  echo "Searched: /usr/local/Cellar/qt@5 and /opt/homebrew/opt/qt@5"; \
	  echo "Try: brew install qt@5"; \
	  exit 1; \
	fi

debug:
	GOTRACEBACK=all GODEBUG='schedtrace=1000,gctrace=1' ./$(REL_MACOS_BIN)

cleanup:
	~/go/bin/staticcheck -checks=U1000 $(SRC)

deps:
	brew install qt@5 golang dylibbundler
	go install github.com/mappu/miqt/cmd/miqt-rcc@latest

res:
	~/go/bin/miqt-rcc -RccBinary $(QT5_BASE)/bin/rcc -Input src/resources.qrc -OutputGo src/resources.qrc.go

