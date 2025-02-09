# build pixlet
FROM alpine:edge AS pixlet-builder

ENV PIXLET_REPO=https://github.com/tavdog/pixlet

RUN apk --no-cache add go=1.23.5-r0 npm=10.9.1-r0 libwebp-dev=1.5.0-r0 git=2.48.1-r0 make=4.4.1-r2 gcc=14.2.0-r5 musl-dev=1.2.5-r9
WORKDIR /
RUN git clone --depth 1 $PIXLET_REPO /pixlet
WORKDIR /pixlet
RUN npm install && npm run build && make build

# build runtime image
FROM alpine:edge AS runtime

# 8000 for main app, 5100, 5101 for pixlet serve iframe 
EXPOSE 8000 5100 5101

ENV PYTHONUNBUFFERED=1
WORKDIR /app

# copy pixlet binary and python dependencies
COPY --from=pixlet-builder /pixlet/pixlet /pixlet/pixlet

RUN apk --no-cache add esptool=4.8.1-r0 --repository=http://dl-cdn.alpinelinux.org/alpine/edge/testing/ && \
    apk --no-cache add python3=3.12.9-r0 \
                       py3-flask=3.0.3-r0 \
                       py3-gunicorn=23.0.0-r0 \
                       py3-dotenv=1.0.1-r1 \
                       libwebp=1.5.0-r0 \
                       libwebpmux=1.5.0-r \
                       libwebpdemux=1.5.0-r \
                       procps-ng=4.0.4-r2 \
                       git=2.48.1-r0

COPY . /app

# start the app
CMD ["./run"]
