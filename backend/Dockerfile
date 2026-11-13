# syntax=docker/dockerfile:1
ARG CLI_SOURCE=cli-build
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS go-base
WORKDIR /src/backend
ARG GOPROXY=https://proxy.golang.org,direct
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY VERSION /src/VERSION

FROM go-base AS backend
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -buildvcs=false -trimpath -ldflags="-s -w -X main.buildVersion=$(cat /src/VERSION)" -o /out/bin/server ./cmd/server

FROM go-base AS cli-binaries
RUN for os in linux darwin windows; do for arch in amd64 arm64; do \
    suffix=; [ "$os" != windows ] || suffix=.exe; \
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -buildvcs=false -trimpath -ldflags="-s -w -X main.version=$(cat /src/VERSION)" -o /out/cli/tvendo-$os-$arch$suffix ./cmd/tvendo || exit 1; \
    done; done

FROM --platform=$BUILDPLATFORM node:24-alpine AS cli-build
WORKDIR /src
COPY --from=cli-binaries /out/cli /out/cli
COPY distribution/ ./distribution/
COPY VERSION LICENSE COPYING.GPL-3 ./
RUN node distribution/package.mjs /out/cli

# Release builds consume the already tested npm and binary artifacts byte-for-byte.
FROM --platform=$BUILDPLATFORM node:24-alpine AS cli-release
WORKDIR /src
COPY distribution/dist/ /out/cli/
COPY distribution/version.mjs ./distribution/
COPY scripts/verify-cli.mjs ./scripts/
COPY VERSION ./
RUN node scripts/verify-cli.mjs /out/cli

FROM ${CLI_SOURCE} AS cli-assets

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
WORKDIR /app
COPY --from=backend /out/bin/server /app/bin/server
COPY --from=frontend /src/frontend/dist /app/frontend
COPY --from=cli-assets /out/cli /app/cli
COPY LICENSE COPYING.GPL-3 /app/
ENV FRONTEND_DIST=/app/frontend CLI_DIST=/app/cli
LABEL org.opencontainers.image.licenses="LGPL-3.0-only"
LABEL org.opencontainers.image.source="https://github.com/dyunwei/tvendo"
USER app
EXPOSE 8080
CMD ["/app/bin/server"]
