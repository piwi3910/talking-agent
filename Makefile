.PHONY: run dev build test web
run:
	go run ./cmd/server
dev:
	npm --prefix web run dev
web:
	npm --prefix web ci
	npm --prefix web run build
build: web
	mkdir -p bin
	go build -o bin/server ./cmd/server
	go build -o bin/mock-backend ./cmd/mock-backend
test:
	go test ./... -timeout 60s
	go vet ./...
