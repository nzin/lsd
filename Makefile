.PHONY: proto generate web build test test-bpf docker up down

proto:
	protoc -I proto --go_out=. --go_opt=module=simplemonitor \
	  --go-grpc_out=. --go-grpc_opt=module=simplemonitor \
	  proto/simplemonitor/v1/ingest.proto

# Recompiles bpf/flows.bpf.c into internal/agent/flows/flows_bpfel.{go,o} (clang with the bpf target
# required, so use a Linux container on macOS). The generated files are committed.
generate:
	docker run --rm -v $(CURDIR):/src -w /src/internal/agent/flows golang:1.27-bookworm \
	  sh -c 'apt-get update -qq && apt-get install -y -qq clang llvm >/dev/null && go generate ./...'

web:
	cd web && npm ci && npm run build

build: web
	CGO_ENABLED=0 go build -o bin/simplemonitor ./cmd/simplemonitor

test:
	go test ./...

test-bpf:
	docker run --rm --privileged -v $(CURDIR):/src -w /src golang:1.27-bookworm \
	  go test -tags bpftest -v ./internal/agent/flows/...

docker:
	docker compose -f deploy/docker-compose.yml build

up:
	docker compose -f deploy/docker-compose.yml up -d --build

down:
	docker compose -f deploy/docker-compose.yml down
