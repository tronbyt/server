# build pixlet
FROM ghcr.io/tavdog/pixlet:latest AS pixlet-builder

# build runtime image
FROM alpine:edge AS runtime

# 8000 for main app, 5100, 5101 for pixlet serve iframe
EXPOSE 8000 5100 5101

ENV PYTHONUNBUFFERED=1
WORKDIR /app

# copy pixlet binary and python dependencies
COPY --from=pixlet-builder /bin/pixlet /pixlet/pixlet

RUN apk --no-cache add esptool --repository=http://dl-cdn.alpinelinux.org/alpine/edge/testing/ && \
    apk --no-cache add python3 py3-flask py3-gunicorn py3-dotenv py3-requests \
                       libwebp libwebpmux libwebpdemux \
                       procps-ng git tzdata ca-certificates

COPY . /app

# start the app
CMD ["./run"]
