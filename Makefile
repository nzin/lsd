.PHONY: proto generate web build test test-bpf test-metrics docs docs-docker docker up down deb deb-server deb-agent linux-binary clean

proto:
	protoc -I proto --go_out=. --go_opt=module=lsd \
	  --go-grpc_out=. --go-grpc_opt=module=lsd \
	  proto/lsd/v1/ingest.proto

# Recompiles bpf/flows.bpf.c into internal/agent/flows/flows_bpfel.{go,o} (clang with the bpf target
# required, so use a Linux container on macOS). The generated files are committed.
generate:
	docker run --rm -v $(CURDIR):/src -w /src/internal/agent/flows golang:1.27-bookworm \
	  sh -c 'apt-get update -qq && apt-get install -y -qq clang llvm >/dev/null && go generate ./...'

# The Go code embeds web/dist (go:embed), which must exist for anything to compile. The UI build wipes
# that folder, so a placeholder is recreated afterwards (and tracked in git): a fresh clone then
# builds and tests without building the UI first.
web/dist/.gitkeep:
	mkdir -p web/dist
	touch web/dist/.gitkeep

web:
	cd web && npm ci && npm run build
	touch web/dist/.gitkeep

build: web
	CGO_ENABLED=0 go build -o bin/lsd ./cmd/lsd

test: web/dist/.gitkeep
	go test ./...

test-bpf:
	docker run --rm --privileged -v $(CURDIR):/src -w /src golang:1.27-bookworm \
	  go test -tags bpftest -v ./internal/agent/flows/...

# docs/install.md -> docs/install.pdf (needs pandoc and pdflatex). The PDF is committed and embedded
# into the program (see `lsd get-documentation`), so re-run this after editing the markdown.
#
# Works with pandoc 3.1 (Ubuntu 24.04) up to 3.8 and newer: the syntax highlighting option was renamed in 3.8.
# On Ubuntu: sudo apt install make pandoc texlive-latex-recommended texlive-latex-extra \
#                 texlive-fonts-recommended lmodern
DOCS_PDF ?= docs/install.pdf
PANDOC_HIGHLIGHT := $(shell pandoc --help 2>/dev/null | grep -q -e '--syntax-highlighting' \
                      && echo --syntax-highlighting=breezedark || echo --highlight-style=breezedark)
PANDOC_PDF_FLAGS := --pdf-engine=pdflatex --template docs/templates/eisvogel.latex \
                    --toc $(PANDOC_HIGHLIGHT) --number-sections
docs:
	@command -v pandoc >/dev/null || { echo "pandoc is required (see the package list above)"; exit 1; }
	pandoc docs/install.md -o $(DOCS_PDF) $(PANDOC_PDF_FLAGS)
	@echo "wrote $(DOCS_PDF)"

# The same, inside an Ubuntu 24.04 container: the reference environment (see deploy/docs.Dockerfile).
docs-docker:
	docker build -q -t lsd-docs -f deploy/docs.Dockerfile deploy
	docker run --rm --user "$$(id -u):$$(id -g)" -e HOME=/tmp -v "$(CURDIR):/src" -w /src lsd-docs make docs

# Ground-truth checks of the CPU / memory numbers (known busy loops, known allocations, /proc cross-check),
# inside a container with a 2-CPU and 2 GB limit.
test-metrics:
	docker run --rm --cpus=2 --memory=2g -v lsd-gomod:/go/pkg/mod -v $(CURDIR):/src -w /src golang:1.27-bookworm \
	  go test -tags containertest -v -count=1 -run 'Cgroup|HostMode' ./internal/agent/hostmetrics/

docker:
	docker compose -f deploy/docker-compose.yml build

up:
	docker compose -f deploy/docker-compose.yml up -d --build

down:
	docker compose -f deploy/docker-compose.yml down

# ---------------------------------------------------------------------------
# Debian packages: lsd-server and lsd-agent
#
#   make deb                      # amd64 (default) -> dist/lsd-{server,agent}_<version>_amd64.deb
#   make deb DEB_ARCH=arm64       # arm64
#   make deb VERSION=1.2.3
#
# Both install under /opt/lsd (in server/ and agent/, so they can live on the same machine),
# each with a systemd unit and a config file in /etc/lsd/. The binary is static, so this also
# works from macOS: if dpkg-deb is not installed it is run inside a Debian container.
# ---------------------------------------------------------------------------
VERSION   ?= 0.1.0
DEB_ARCH  ?= amd64
DIST      := dist
STAGE     := $(DIST)/stage
LINUX_BIN := bin/lsd-linux-$(DEB_ARCH)

ifneq ($(shell command -v dpkg-deb 2>/dev/null),)
dpkg_build = cd $(DIST) && dpkg-deb --root-owner-group -Zgzip --build $(1) $(2)
else
dpkg_build = docker run --rm -v "$(CURDIR)/$(DIST):/work" -w /work debian:bookworm-slim \
	dpkg-deb --root-owner-group -Zgzip --build $(1) $(2)
endif

# Static Linux binary (web UI embedded) for the target architecture.
linux-binary: web
	CGO_ENABLED=0 GOOS=linux GOARCH=$(DEB_ARCH) go build -trimpath \
	  -ldflags "-s -w -X main.version=$(VERSION)" -o $(LINUX_BIN) ./cmd/lsd

# $(1) = package name, $(2) = role (server | agent)
define BUILD_DEB
	rm -rf $(STAGE)/$(1)
	mkdir -p $(STAGE)/$(1)/DEBIAN $(STAGE)/$(1)/opt/lsd/$(2) \
	         $(STAGE)/$(1)/lib/systemd/system $(STAGE)/$(1)/etc/lsd
	install -m 0755 $(LINUX_BIN) $(STAGE)/$(1)/opt/lsd/$(2)/lsd
	install -m 0644 packaging/$(2)/$(1).service $(STAGE)/$(1)/lib/systemd/system/$(1).service
	install -m 0600 packaging/$(2)/$(2).env $(STAGE)/$(1)/etc/lsd/$(2).env
	install -m 0644 packaging/$(2)/conffiles $(STAGE)/$(1)/DEBIAN/conffiles
	install -m 0755 packaging/$(2)/postinst packaging/$(2)/prerm packaging/$(2)/postrm $(STAGE)/$(1)/DEBIAN/
	sed -e 's/@VERSION@/$(VERSION)/' -e 's/@ARCH@/$(DEB_ARCH)/' \
	    -e "s/@SIZE@/$$(du -sk $(STAGE)/$(1)/opt | cut -f1)/" \
	    packaging/$(2)/control > $(STAGE)/$(1)/DEBIAN/control
	$(call dpkg_build,stage/$(1),$(1)_$(VERSION)_$(DEB_ARCH).deb)
endef

deb-server: linux-binary
	$(call BUILD_DEB,lsd-server,server)

deb-agent: linux-binary
	$(call BUILD_DEB,lsd-agent,agent)

deb: deb-server deb-agent
	@ls -l $(DIST)/*.deb

clean:
	rm -rf bin $(DIST)
