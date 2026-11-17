# syntax=docker/dockerfile:1
#
# The sherwood container: the Go application and the Python broker helper in
# one image. Build it with `docker build -t sherwood .` or through
# compose.example.yaml.
#
# Every base image is pinned by digest. The tag beside each digest is only a
# label for people; the digest is what is pulled. Dependabot proposes digest
# updates.
#
# The image holds no settings, strategy or broker file and no database. Those
# live in the application-data folder the Compose file mounts at /data.

# ---- Go build ---------------------------------------------------------------
FROM golang:1.26-trixie@sha256:cff05dd6b81c901f2e8249eb6f91c69a60451d0f12c26bfef08e476a85b32d4d AS build

WORKDIR /src

# Modules first, so a code change does not download them again.
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY cmd ./cmd
COPY internal ./internal

ARG VERSION=dev
# The SQLite driver is pure Go, so no cgo is needed and the binary is static.
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/sherwood ./cmd/sherwood

# ---- Runtime ----------------------------------------------------------------
FROM python:3.13-slim-trixie@sha256:e48f59cdba69a3cbd9ed2cf0f348d90d45a8d350d98134cf1070bc106ee9ed90

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PYTHONPATH=/app/brokerhelper/src

WORKDIR /app

# The broker helper's runtime dependencies, each checked against its hash.
COPY brokerhelper/requirements.lock ./brokerhelper/requirements.lock
RUN pip install --require-hashes --no-deps -r brokerhelper/requirements.lock

COPY brokerhelper/src ./brokerhelper/src
COPY --from=build /out/sherwood /usr/local/bin/sherwood

# A fixed, unprivileged user. The mounted /data folder must be writable by
# this user id, or the Compose file's `user:` must name the folder's owner.
RUN groupadd --system --gid 10001 sherwood \
 && useradd --system --uid 10001 --gid sherwood --no-create-home \
      --home-dir /nonexistent --shell /usr/sbin/nologin sherwood \
 && mkdir -p /data \
 && chown sherwood:sherwood /data
USER 10001:10001

VOLUME ["/data"]

# The web port. No web server listens yet; a later part adds it.
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/sherwood"]
