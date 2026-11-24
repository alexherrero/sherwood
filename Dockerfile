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

# ---- Python dependencies ----------------------------------------------------
# The same Python minor version as the runtime image below (Debian 13's 3.13),
# so the compiled wheels match it. pip and this stage stay out of the image.
FROM python:3.13-slim-trixie@sha256:e48f59cdba69a3cbd9ed2cf0f348d90d45a8d350d98134cf1070bc106ee9ed90 AS pydeps

# The broker helper's runtime dependencies, each checked against its hash,
# into a folder of their own. No bytecode: the root filesystem is read-only,
# so Python compiles in memory at each start, which takes well under a second.
COPY brokerhelper/requirements.lock /tmp/requirements.lock
RUN PIP_NO_CACHE_DIR=1 PIP_DISABLE_PIP_VERSION_CHECK=1 \
      pip install --require-hashes --no-deps --no-compile \
        --target /out/deps -r /tmp/requirements.lock \
 && find /out/deps -depth -name '__pycache__' -exec rm -rf {} + \
 && mkdir -p /out/data \
 && chown 10001:10001 /out/data

# ---- Runtime ----------------------------------------------------------------
# Distroless: Python and the libraries it needs, with no shell, no package
# manager and no pip. Nothing is ever run inside the container but sherwood
# and its broker helper (`docker exec ... sherwood ...` and
# `docker exec ... python3 -m brokerhelper ...` both still work).
FROM gcr.io/distroless/python3-debian13@sha256:4d4b3e43e67a4d3e149d68d30a68b7d48c289e4e1dabf8201271e6252805c9dd

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PYTHONPATH=/app/deps:/app/brokerhelper/src

WORKDIR /app

COPY --from=pydeps /out/deps ./deps
COPY brokerhelper/src ./brokerhelper/src
COPY --from=build /out/sherwood /usr/local/bin/sherwood

# A fixed, unprivileged user id. The mounted /data folder must be writable by
# it, or the Compose file's `user:` must name the folder's owner.
COPY --from=pydeps /out/data /data
USER 10001:10001

VOLUME ["/data"]

# The web port. No web server listens yet; a later part adds it.
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/sherwood"]
