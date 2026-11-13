FROM node:24-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src/backend
ARG GOPROXY=https://proxy.golang.org,direct
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/bin/server ./cmd/server
RUN for os in linux darwin windows; do for arch in amd64 arm64; do \
    suffix=; [ "$os" != windows ] || suffix=.exe; \
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags='-s -w -X main.version=0.1.0' -o /out/cli/tvendo-$os-$arch$suffix ./cmd/tvendo || exit 1; \
    done; done

FROM node:24-alpine AS cli-package
COPY --from=backend /out/cli /out/cli
COPY distribution/launcher.cjs distribution/package.mjs /pack/
RUN node /pack/package.mjs /out/cli 0.1.0

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
WORKDIR /app
COPY --from=backend /out/bin/server /app/bin/server
COPY --from=frontend /src/frontend/dist /app/frontend
COPY --from=cli-package /out/cli /app/cli
ENV FRONTEND_DIST=/app/frontend CLI_DIST=/app/cli
USER app
EXPOSE 8080
CMD ["/app/bin/server"]
