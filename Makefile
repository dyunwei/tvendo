.PHONY: cli-build cli-release build backend-build test dev deps-up deps-down up down
build:
	cd frontend && npm ci && npm run build
	$(MAKE) backend-build
backend-build:
	cd backend && go build -buildvcs=false -o bin/server ./cmd/server
test:
	cd backend && go test -buildvcs=false ./...
dev:
	cd backend && go run -buildvcs=false ./cmd/server
deps-up:
	docker compose -p tvendo-dev -f compose.yaml -f compose.dev.yaml up -d postgres redis
deps-down:
	docker compose -p tvendo-dev -f compose.yaml -f compose.dev.yaml down
up:
	docker compose up -d --build
down:
	docker compose down

cli-build:
	cd backend && go build -buildvcs=false -o bin/tvendo ./cmd/tvendo
cli-release:
	sh scripts/build-cli.sh 0.1.0
