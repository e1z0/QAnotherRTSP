FROM golang:1.23-bookworm

RUN DEBIAN_FRONTEND=noninteractive apt-get update && \
    apt-get install -qyy gnupg2 ca-certificates && \
    apt-get clean
    
RUN DEBIAN_FRONTEND=noninteractive \
    echo "deb https://pkg.mxe.cc/repos/apt buster main" >/etc/apt/sources.list.d/mxeapt.list && \
    apt-key adv --keyserver keyserver.ubuntu.com --recv-keys 86B72ED9 && \
    apt-get update && \
    apt-get install -qyy mxe-i686-w64-mingw32.static-qt5 mxe-i686-w64-mingw32.static-x265 yasm wget && \
    apt-get clean

ENV PATH=/usr/lib/mxe/usr/bin:$PATH

ENV CXX=i686-w64-mingw32.static-g++
ENV CC=i686-w64-mingw32.static-gcc
ENV PKG_CONFIG=i686-w64-mingw32.static-pkg-config
ENV GOOS=windows
ENV CGO_ENABLED=1
ENV GOFLAGS=-buildvcs=false
# enable build github.com/mappu/miqt/qt/mainthread because it requires c++11 standard
ENV CGO_CXXFLAGS=-std=c++11

RUN cd /tmp && wget -q https://github.com/FFmpeg/FFmpeg/archive/refs/tags/n7.0.3.tar.gz -O ffmpeg-7.0.3.tar.gz && \
    tar xzf ffmpeg-7.0.3.tar.gz && \
    cd FFmpeg-n7.0.3 && \
    ./configure \
    --prefix="/usr/lib/mxe/usr/i686-w64-mingw32.static" \
    --pkgconfigdir="/usr/lib/mxe/usr/i686-w64-mingw32.static/lib/pkgconfig" \
    --arch=x86_64 --target-os=mingw32 --cross-prefix=i686-w64-mingw32.static- \
    --pkg-config-flags=--static \
    --extra-cflags="-I/usr/lib/mxe/usr/i686-w64-mingw32.static/include" \
    --extra-ldflags=-static \
    --enable-static --disable-shared --disable-debug --enable-pic \
    --disable-librsvg --disable-xlib --disable-libxcb --disable-sdl2 \
    --disable-vulkan --disable-opencl --disable-libdrm \
    --disable-autodetect \
    --disable-programs --disable-doc \
    --disable-everything \
    --enable-network \
    --enable-protocol=file,pipe,tcp,udp,rtp,rtsp,tls,https \
    --enable-demuxer=rtsp,sdp,rtp \
    --enable-decoder=h264,hevc,mpeg4,mjpeg,aac,pcm_alaw,pcm_mulaw,opus \
    --enable-parser=h264,hevc,aac,mpeg4video,mjpeg,opus \
    --enable-bsf=h264_mp4toannexb,hevc_mp4toannexb,aac_adtstoasc \
    --enable-swscale --enable-swresample \
    --enable-libx265 --enable-gpl && \
    make -j"$(nproc)" && \
    make install
