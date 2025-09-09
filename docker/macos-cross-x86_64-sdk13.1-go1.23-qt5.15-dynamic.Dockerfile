FROM crazymax/osxcross:13.1-debian AS osxcross

FROM debian:bookworm

COPY --from=osxcross /osxcross /osxcross

RUN DEBIAN_FRONTEND=noninteractive apt-get update && \
	apt-get install --no-install-recommends -qyy \
		clang \
		lld \
		libc6-dev \
		openssl \
		bzip2 \
		ca-certificates \
		curl \
                python3 \
                zip \
		pkg-config wget nano procps make yasm unzip && \
    apt-get clean

ENV PATH="/osxcross/bin:$PATH"
ENV LD_LIBRARY_PATH="/osxcross/lib"
#:$LD_LIBRARY_PATH"

# The oldest macOS target with a working Qt 5.15 build on macports.org is High
# Sierra (10.13)
# @ref https://ports.macports.org/port/qt5-qtbase/details/
#
# Go 1.19 and Go 1.20 are the last versions of Go that can target macOS 10.13.
# For later versions of Go, a higher MACOSX_DEPLOYMENT_TARGET version can be set.
# @ref https://tip.golang.org/doc/go1.20#darwin
ENV MACOSX_DEPLOYMENT_TARGET=13.1

# Preemptively mark some dependencies as installed that don't seem to download properly
RUN /usr/bin/env UNATTENDED=1 osxcross-macports update-cache && UNATTENDED=1 osxcross-macports \
    fake-install py313 py313-packaging xorg xrender curl-ca-bundle graphviz librsvg

# Install Qt 5.15 and dependencies
RUN /usr/bin/env UNATTENDED=1 osxcross-macports install qt5-qtbase x264 x265 libvpx libvpx-devel

RUN rmdir /opt/ && \
	ln -s /osxcross/macports/pkgs/opt /opt

RUN curl -L -o /tmp/golang.tar.gz https://go.dev/dl/go1.23.1.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf /tmp/golang.tar.gz

# prefix for all tools
ENV TOOLCHAINPREFIX=x86_64-apple-darwin22.2
RUN ln -sf `which $TOOLCHAINPREFIX-otool` /usr/bin/otool && \
    ln -sf `which $TOOLCHAINPREFIX-install_name_tool` /usr/bin/install_name_tool && \
    ln -sf `which $TOOLCHAINPREFIX-codesign_allocate` /usr/bin/codesign

ENV CC=x86_64-apple-darwin22.2-clang
ENV CXX=x86_64-apple-darwin22.2-clang++
ENV GOOS=darwin
ENV GOARCH=amd64
ENV CGO_ENABLED=1
ENV PATH=/usr/local/go/bin:$PATH
ENV PKG_CONFIG_PATH=/opt/local/libexec/qt5/lib/pkgconfig/
ENV CGO_CXXFLAGS="-Wno-ignored-attributes -D_Bool=bool"

COPY ./build-ffmpeg /build-ffmpeg
RUN /build-ffmpeg
