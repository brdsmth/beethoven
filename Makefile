.PHONY: all web build test test-short fake-image clean

all: build

web:
	cd web && bun install --frozen-lockfile && bun run build

build: web
	go build -o bin/beethoven ./cmd/beethoven

test:
	go test -race ./...

test-short:
	go test -race -short ./...

# Sandbox image with a fake `claude`, for UI work without API calls:
#   BEETHOVEN_IMAGE=beethoven-fake:latest bin/beethoven serve
fake-image:
	docker build -t beethoven-fake:latest internal/session/testdata/fakeagent

clean:
	rm -rf bin web/node_modules
	find web/dist -mindepth 1 ! -name .gitkeep -delete
