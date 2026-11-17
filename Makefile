.PHONY: proto generate web build test test-bpf docker up down deb deb-server deb-agent linux-binary clean

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

# ---------------------------------------------------------------------------
# Debian packages: simplemonitor-server and simplemonitor-agent
#
#   make deb                      # amd64 (default) -> dist/simplemonitor-{server,agent}_<version>_amd64.deb
#   make deb DEB_ARCH=arm64       # arm64
#   make deb VERSION=1.2.3
#
# Both install under /opt/simplemonitor (in server/ and agent/, so they can live on the same machine),
# each with a systemd unit and a config file in /etc/simplemonitor/. The binary is static, so this also
# works from macOS: if dpkg-deb is not installed it is run inside a Debian container.
# ---------------------------------------------------------------------------
VERSION   ?= 0.1.0
DEB_ARCH  ?= amd64
DIST      := dist
STAGE     := $(DIST)/stage
LINUX_BIN := bin/simplemonitor-linux-$(DEB_ARCH)

ifneq ($(shell command -v dpkg-deb 2>/dev/null),)
dpkg_build = cd $(DIST) && dpkg-deb --root-owner-group -Zgzip --build $(1) $(2)
else
dpkg_build = docker run --rm -v "$(CURDIR)/$(DIST):/work" -w /work debian:bookworm-slim \
	dpkg-deb --root-owner-group -Zgzip --build $(1) $(2)
endif

# Static Linux binary (web UI embedded) for the target architecture.
linux-binary: web
	CGO_ENABLED=0 GOOS=linux GOARCH=$(DEB_ARCH) go build -trimpath \
	  -ldflags "-s -w -X main.version=$(VERSION)" -o $(LINUX_BIN) ./cmd/simplemonitor

# $(1) = package name, $(2) = role (server | agent)
define BUILD_DEB
	rm -rf $(STAGE)/$(1)
	mkdir -p $(STAGE)/$(1)/DEBIAN $(STAGE)/$(1)/opt/simplemonitor/$(2) \
	         $(STAGE)/$(1)/lib/systemd/system $(STAGE)/$(1)/etc/simplemonitor
	install -m 0755 $(LINUX_BIN) $(STAGE)/$(1)/opt/simplemonitor/$(2)/simplemonitor
	install -m 0644 packaging/$(2)/$(1).service $(STAGE)/$(1)/lib/systemd/system/$(1).service
	install -m 0600 packaging/$(2)/$(2).env $(STAGE)/$(1)/etc/simplemonitor/$(2).env
	install -m 0644 packaging/$(2)/conffiles $(STAGE)/$(1)/DEBIAN/conffiles
	install -m 0755 packaging/$(2)/postinst packaging/$(2)/prerm packaging/$(2)/postrm $(STAGE)/$(1)/DEBIAN/
	sed -e 's/@VERSION@/$(VERSION)/' -e 's/@ARCH@/$(DEB_ARCH)/' \
	    -e "s/@SIZE@/$$(du -sk $(STAGE)/$(1)/opt | cut -f1)/" \
	    packaging/$(2)/control > $(STAGE)/$(1)/DEBIAN/control
	$(call dpkg_build,stage/$(1),$(1)_$(VERSION)_$(DEB_ARCH).deb)
endef

deb-server: linux-binary
	$(call BUILD_DEB,simplemonitor-server,server)

deb-agent: linux-binary
	$(call BUILD_DEB,simplemonitor-agent,agent)

deb: deb-server deb-agent
	@ls -l $(DIST)/*.deb

clean:
	rm -rf bin $(DIST)
