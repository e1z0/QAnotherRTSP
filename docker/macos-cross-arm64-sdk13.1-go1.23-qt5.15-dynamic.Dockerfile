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
		pkg-config wget nano procps make yasm unzip file git && \
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
RUN /usr/bin/env UNATTENDED=1 osxcross-macports -arm64 install qt5-qtbase x264 x265 \
    libvpx libvpx-devel libde265 x265 openh264 codec2 openjpeg libtheora aom

RUN rmdir /opt/ && \
	ln -s /osxcross/macports/pkgs/opt /opt

RUN curl -L -o /tmp/golang.tar.gz https://go.dev/dl/go1.23.1.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf /tmp/golang.tar.gz

# prefix for all tools
ENV TOOLCHAINPREFIX=arm64-apple-darwin22.2
RUN ln -sf `which $TOOLCHAINPREFIX-otool` /usr/bin/otool && \
    ln -sf `which $TOOLCHAINPREFIX-install_name_tool` /usr/bin/install_name_tool && \
    ln -sf `which $TOOLCHAINPREFIX-codesign_allocate` /usr/bin/codesign

ENV CC=arm64-apple-darwin22.2-clang
ENV CXX=arm64-apple-darwin22.2-clang++
ENV GOOS=darwin
ENV GOARCH=arm64
ENV CGO_ENABLED=1
ENV PATH=/usr/local/go/bin:$PATH
ENV CGO_CXXFLAGS="-Wno-ignored-attributes -D_Bool=bool"


ENV PKG_CONFIG_PATH=/opt/local/libexec/qt5/lib/pkgconfig:/osxcross/macports/pkgs/opt/local/lib/pkgconfig:/osxcross/macports/pkgs/opt/local/libexec/ffmpeg7/lib/pkgconfig

RUN wget -q https://ffmpeg.org/releases/ffmpeg-7.0.3.tar.gz -O /tmp/ffmpeg.tar.gz && tar xzf /tmp/ffmpeg.tar.gz -C /tmp/ && \
    cd /tmp/ffmpeg-7.0.3 && \
    ./configure \
    --prefix=/osxcross/macports/pkgs/opt/local/libexec/ffmpeg7 \
    --enable-cross-compile \
    --target-os=darwin \
    --arch=arm64 \
    --cc="arm64-apple-darwin22.2-clang" --cxx="arm64-apple-darwin22.2-clang++" \
    --ar="arm64-apple-darwin22.2-ar" --ranlib="arm64-apple-darwin22.2-ranlib" \
    --nm="arm64-apple-darwin22.2-nm" --strip="arm64-apple-darwin22.2-strip" \
    --install-name-dir='@rpath' \
    --sysroot="/osxcross/SDK/MacOSX13.1.sdk" \
    --extra-cflags="-I/osxcross/macports/pkgs/opt/local/include -isysroot /osxcross/SDK/MacOSX13.1.sdk -mmacosx-version-min=13.0 -std=c11" \
    --extra-ldflags="-L/osxcross/macports/pkgs/opt/local/lib -isysroot /osxcross/SDK/MacOSX13.1.sdk -mmacosx-version-min=13.0 -Wl,-headerpad_max_install_names" \
    --host-cc="clang" --host-cflags="-std=c11" \
    --disable-librsvg --disable-xlib --disable-libxcb \
    --disable-vulkan --disable-opencl \
    --disable-doc --disable-static --enable-shared \
    --disable-programs \
    --enable-videotoolbox \
    --enable-audiotoolbox \
    --enable-network --enable-securetransport \
    --enable-swscale --enable-swresample --enable-nonfree \
    --enable-libfreetype \
    --enable-libaom \
    --enable-libcodec2 \
    --enable-libopenjpeg \
    --enable-libmp3lame --enable-libopus \
    --enable-libtheora --enable-libx264 \
    --enable-libx265 \
    --enable-opengl --enable-gpl && \
    make && make install

