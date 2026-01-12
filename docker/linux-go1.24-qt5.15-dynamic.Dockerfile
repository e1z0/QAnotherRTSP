ARG TARGETARCH
ARG TARGETPLATFORM
FROM debian:trixie


RUN DEBIAN_FRONTEND=noninteractive apt-get update && \
    apt-get install -qyy file nano qtbase5-dev ffmpeg fuse wget \
    libasound2-dev libavcodec-dev golang-go libavcodec61 libavdevice-dev libavdevice61 \
    libavfilter-dev libavfilter10 libavformat-dev libavformat61 \
    libswresample-dev libswresample5 libswscale-dev libswscale8 libavutil-dev libavutil59 && \
    apt-get clean


