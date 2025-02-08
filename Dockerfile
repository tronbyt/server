# build pixlet
FROM golang:1.23-alpine3.21 AS pixlet-builder

ENV PIXLET_REPO=https://github.com/tavdog/pixlet

RUN apk --no-cache add npm libwebp-dev git make gcc musl-dev
WORKDIR /
RUN git clone --depth 1 $PIXLET_REPO /pixlet
WORKDIR /pixlet
RUN npm install && npm run build && make build

# package python dependencies
FROM python:3.13-alpine3.21 AS python-builder

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

WORKDIR /app
COPY requirements.txt .
RUN apk --no-cache add gcc python3-dev musl-dev linux-headers && \
    pip wheel --no-cache-dir --wheel-dir /app/wheels -r requirements.txt

# build runtime image
FROM python:3.13-alpine3.21 AS runtime

# 8000 for main app, 5100, 5101 for pixlet serve iframe 
EXPOSE 8000 5100 5101

WORKDIR /app

# copy pixlet binary and python dependencies
COPY --from=pixlet-builder /pixlet/pixlet /pixlet/pixlet
COPY --from=python-builder /app/wheels /wheels
COPY --from=python-builder /app/requirements.txt /app

RUN pip install --no-cache /wheels/* && \
    apk --no-cache add libwebp libwebpmux libwebpdemux procps-ng git

COPY . /app

# start the app
CMD ["./run"]
