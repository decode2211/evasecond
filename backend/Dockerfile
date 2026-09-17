# Multi-stage build: the first stage compiles the Go binary, the second
# stage is just that one binary sitting in an otherwise-empty, non-root
# image. Nothing needed only for compiling (the Go toolchain, source code)
# ships in the final image, which keeps it small and reduces what an
# attacker could find useful if they ever got into the container.

FROM golang:1.23-alpine AS build
WORKDIR /src

# Copy just the module files first so Docker can cache the downloaded
# dependencies as their own layer — they only need to be re-downloaded when
# go.mod/go.sum actually change, not on every source-code edit.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 produces a fully static binary with no C library
# dependency, which is what lets the final stage be an image with no
# libc at all. The pgx Postgres driver is pure Go, so this works without
# any special handling.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# distroless "static" images contain nothing but CA certificates and a
# handful of /etc files — no shell, no package manager, nothing an
# attacker could use even if they found a way to execute arbitrary
# commands. The ":nonroot" tag also pre-creates and switches to an
# unprivileged user, so the server never runs as root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/server"]
