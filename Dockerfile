FROM golang:1.18-alpine as builder
RUN apk add --no-cache gstreamer-dev gst-plugins-base-dev musl-dev gcc
WORKDIR /src
ADD . .
RUN go build -o /rtp-audio-processor

FROM alpine:3.16
RUN apk add --no-cache gst-plugins-good gst-plugins-bad
COPY --from=builder /rtp-audio-processor /usr/bin
ENTRYPOINT ["rtp-audio-processor"]
