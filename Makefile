APP := AnotherRTSP
SRC := ./src
BUILD_FILE := BUILD
VERSION := $(shell cat VERSION)
BUILD := $(shell cat $(BUILD_FILE))
LINES := $(shell wc -l $(SRC)/*.go | grep total | awk '{print $$1}')
BINARY := another-rtsp
#QT_PATH_MAC := /opt/homebrew/opt/qt@5
REL_MACOS_BIN := $(BINARY)-mac
REL_WINDOWS_BIN := $(BINARY)-win.exe
REL_DIR := release
MAC_APP_DIR := $(REL_DIR)/$(APP).app
ARCH := $(shell uname -m)
UID ?= $(shell id -u)
GID ?= $(shell id -g)
WINDOCKERIMAGE := qanotherrtsp-win64-cross-go1.23-qt5.15-static:latest
OSXINTELDOCKER := qanotherrtsp-macos-cross-x86_64-sdk13.1-go1.24.3-qt5.15-dynamic:latest
OSXARMDOCKER := qanotherrtsp-macos-cross-arm64-sdk13.1-go1.24.3-qt5.15-dynamic:latest
OS := $(shell uname -s)

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


#ifneq ($(wildcard /usr/local/Cellar/qt@5),)
#  QT5_BASE := $(lastword $(sort $(wildcard /usr/local/Cellar/qt@5/*)))
#else ifneq ($(wildcard /opt/homebrew/opt/qt@5),)
#  QT5_BASE := /opt/homebrew/opt/qt@5
#else
#  QT5_BASE :=
#endif


all: build_mac

debug:
	GOTRACEBACK=all GODEBUG='schedtrace=1000,gctrace=1' ./$(REL_MACOS_BIN)

cleanup:
	~/go/bin/staticcheck -checks=U1000 $(SRC)
deps:
	brew install qt@5 golang dylibbundler
	go install github.com/mappu/miqt/cmd/miqt-rcc@latest

res:
	~/go/bin/miqt-rcc -RccBinary $(QT5_BASE)/bin/rcc -Input src/resources.qrc -OutputGo src/resources.qrc.go

# Windows x86_64 docker builder
docker_win: ## Make a Windows builder docker container
	@if ! docker image inspect "$(WINDOCKERIMAGE)" > /dev/null 2>&1; then \
	echo "Image not found, building..."; \
	docker build -f docker/win64-cross-go1.23-qt5.15-static.Dockerfile -t "$(WINDOCKERIMAGE)" docker/; \
	else \
	echo "Docker image already built, using it..."; \
	fi
docker_mactel: ## Make a MacOS Intel builder docker container
	@if ! docker image inspect "$(OSXINTELDOCKER)" > /dev/null 2>&1; then \
	echo "Image not found, building..."; \
	docker build -f docker/macos-cross-x86_64-sdk13.1-go1.23-qt5.15-dynamic.Dockerfile -t "$(OSXINTELDOCKER)" docker/; \
	else \
	echo "Docker image already built, using it..."; \
	fi
docker_macarm: # Make a MacOS Arm builder docker container
	@if ! docker image inspect "$(OSXARMDOCKER)" > /dev/null 2>&1; then \
	echo "Image not found, building..."; \
	docker build -f docker/macos-cross-arm64-sdk13.1-go1.23-qt5.15-dynamic.Dockerfile -t "$(OSXARMDOCKER)" docker/; \
	else \
	echo "Docker image already built, using it..."; \
	fi
docker_mactel_clean:
	docker image rm $(OSXINTELDOCKER)
docker_macarm_clean:
	docker image rm $(OSXARMDOCKER)
docker_win_clean:
	docker image rm $(WINDOCKERIMAGE)

docker_build_win: ## Enter docker build environment for Windows
	 docker run --rm --init -i --user $(UID):$(UID) \
		-v ${HOME}/go/pkg/mod:/go/pkg/mod \
		-e GOMODCACHE=/go/pkg/mod \
		-v ${HOME}/.cache/go-build:/.cache/go-build \
		-e GOCACHE=/.cache/go-build \
		-v ${PWD}:/src/QAnotherRTSP \
		-w /src/QAnotherRTSP \
		-e HOME=/tmp \
		$(WINDOCKERIMAGE) \
		go build -ldflags "-X main.version=${VERSION} \
		-X main.build=${BUILD} \
		-X main.lines=$(LINES) \
		-X main.debugging=false \
		-s -w -H windowsgui" --tags=windowsqtstatic -o $(REL_WINDOWS_BIN) ./src/
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
	#install_name_tool -add_rpath "@executable_path/lib" $(REL_MACOS_BIN)

docker_build_mactel: ## Build project for MacOS Intel
	docker run --rm --init -i --user $(UID):$(UID) \
		-v ${HOME}/go/pkg/mod:/go/pkg/mod \
		-e GOMODCACHE=/go/pkg/mod \
		-v ${HOME}/.cache/go-build:/.cache/go-build \
		-e GOOS=darwin \
		-e GOARCH=amd64 \
		-e GOCACHE=/.cache/go-build \
		-v ${PWD}:/src/QAnotherRTSP \
		-w /src/QAnotherRTSP \
		-e HOME=/tmp \
		$(OSXINTELDOCKER) \
                ./scripts/dockerbuild

docker_build_macarm: ## Build project for MacOS arm
	docker run --rm --init -i --user $(UID):$(UID) \
		-v ${HOME}/go/pkg/mod:/go/pkg/mod \
		-e GOMODCACHE=/go/pkg/mod \
		-v ${HOME}/.cache/go-build:/.cache/go-build \
		-e GOOS=darwin \
		-e GOARCH=arm64 \
		-e GOCACHE=/.cache/go-build \
		-v ${PWD}:/src/QAnotherRTSP \
		-w /src/QAnotherRTSP \
		-e HOME=/tmp \
 		$(OSXARMDOCKER) \
		./scripts/dockerbuild

#build_err:
#	CGO_ENABLED=1 \
#	PATH="$(QT5_BASE)/bin:$$PATH" \
#	LDFLAGS="-L$(QT5_BASE)/lib -L/Applications/VLC.app/Contents/MacOS/lib" \
#	CPPFLAGS="-I$(QT5_BASE)/include" \
#	GOEXPERIMENT=cgocheck2 \
#	CGO_LDFLAGS="-L/opt/homebrew/opt/ffmpeg/lib/" \
#	CGO_CFLAGS="-I/opt/homebrew/opt/ffmpeg/include/" \
#	PKG_CONFIG_PATH="$(QT5_BASE)/lib/pkgconfig:/opt/homebrew/opt/ffmpeg/pkgconfig" \
#	go build -gcflags=all=-e -ldflags '-v -s -w' -o $(REL_MACOS_BIN) .

release_mac: ## Release build for MacOS Apple Silicon/Intel (local mac machine only)
	[ -d $(MAC_APP_DIR) ] && rm -rf $(MAC_APP_DIR) || true
	[ -f $(REL_DIR)/$(APP)-$(ARCH)-Mac.zip ] && rm $(REL_DIR)/$(APP)-$(ARCH)-Mac.zip || true
	[ -f $(REL_DIR)/$(APP)-$(ARCH).dmg ] && rm $(REL_DIR)/$(APP)-$(ARCH).dmg || true
	cp -r resources/macos-skeleton $(MAC_APP_DIR)
	mkdir $(MAC_APP_DIR)/Contents/{MacOS,Frameworks,libs}
	cp $(REL_MACOS_BIN) $(MAC_APP_DIR)/Contents/MacOS/$(BINARY)
	# copy all necessary ffmpeg folders
	#cp -R $(FFMPEG_PREFIX)/lib/*.dylib $(MAC_APP_DIR)/Contents/Frameworks/
	#chmod -R 755 $(MAC_APP_DIR)/Contents/MacOS/*
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
	docker run --rm --init -i -t --user $(UID):$(UID) \
		-v ${HOME}/go/pkg/mod:/go/pkg/mod \
		-e GOMODCACHE=/go/pkg/mod \
		-v ${HOME}/.cache/go-build:/.cache/go-build \
		-e GOOS=darwin \
		-e GOARCH=amd64 \
		-e GOCACHE=/.cache/go-build \
		-v ${PWD}:/src/QAnotherRTSP \
		-w /src/QAnotherRTSP \
		-e HOME=/tmp \
		$(OSXINTELDOCKER) \
		bash

# Enter MacOS ARM64 docker
docker_macarm_enter: ## Enter docker build environment for MacOS ARM
	docker run --rm --init -i -t --user $(UID):$(UID) \
		-v ${HOME}/go/pkg/mod:/go/pkg/mod \
		-e GOMODCACHE=/go/pkg/mod \
		-v ${HOME}/.cache/go-build:/.cache/go-build \
		-e GOOS=darwin \
		-e GOARCH=arm64 \
		-e GOCACHE=/.cache/go-build \
		-v ${PWD}:/src/QAnotherRTSP \
		-w /src/QAnotherRTSP \
		-e HOME=/tmp \
		$(OSXARMDOCKER) \
		bash

# Enter Windows docker
docker_win_enter: ## Enter docker build environment for Windows
	docker run --rm --init -i -t --user $(UID):$(UID) \
		-v ${HOME}/go/pkg/mod:/go/pkg/mod \
		-e GOMODCACHE=/go/pkg/mod \
		-v ${HOME}/.cache/go-build:/.cache/go-build \
		-e GOCACHE=/.cache/go-build \
		-v ${PWD}:/src/QAnotherRTSP \
		-w /src/QAnotherRTSP \
		-e HOME=/tmp \
		$(WINDOCKERIMAGE) \
		bash

check-qt:
	@if [ -z "$(QT5_PREFIX)" ]; then \
	  echo "Error: Qt5 not found."; \
	  echo "Searched: /usr/local/Cellar/qt@5 and /opt/homebrew/opt/qt@5"; \
	  echo "Try: brew install qt@5"; \
	  exit 1; \
	fi
