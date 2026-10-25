.DEFAULT_GOAL := help
.NOTPARALLEL:

GO ?= go
UV ?= uv
PYTHON ?= python3
ADB ?= adb
STATION_IP ?=
JOBS ?= 2

-include config.mk
export GO UV PYTHON JOBS MAKE ADB ZIG EXTRA_PATH

VERSION := $(shell sed -n 's/^const Version = "\(.*\)"/\1/p' internal/voice/paths.go)
RELEASE := dist/station-xiaozhi-$(VERSION)

.PHONY: help setup fmt test vet check build clean release install status start stop bind uninstall logs shell

help:
	@printf '%s\n' \
	  'setup      Prepare Go modules, pinned Zig and ARM Opus (no system packages)' \
	  'fmt        Format Go source in place' \
	  'test       Run native race tests (requires host Opus, pkg-config and cc)' \
	  'vet        Vet native source (requires host Opus, pkg-config and cc)' \
	  'check      Check formatting, scripts, tests and vet without formatting writes' \
	  'build      Build ARM build/station-xiaozhi' \
	  'release    Build dist/station-xiaozhi-$(VERSION)/, its .zip and the yam .yap' \
	  'install    Release, then install on STATION_IP with the bundled installer' \
	  'status     Show whether the assistant runs, and its recent log' \
	  'start      Start and enable autostart' \
	  'stop       Stop and disable autostart' \
	  'bind       Show the xiaozhi.me binding code and wait for it' \
	  'uninstall  Remove the assistant from the Station' \
	  'logs       Follow the Station log' \
	  'shell      Open the Station shell' \
	  'clean      Remove build and dist output; keep config and toolchain cache' \
	  '' \
	  'Copy config.example.mk to config.mk and set STATION_IP before device commands.'

setup:
	$(GO) list -mod=readonly -tags=nolibopusfile -deps ./cmd/station-xiaozhi >/dev/null
	sh tools/build-arm.sh setup

fmt:
	"$$($(GO) env GOROOT)/bin/gofmt" -w cmd internal

define host-check
	@command -v cc >/dev/null || { echo 'Install a host C compiler (cc) before native checks.' >&2; exit 1; }
	@pkg-config --exists opus || { echo 'Install host Opus and pkg-config before native checks.' >&2; exit 1; }
endef

test:
	$(host-check)
	CGO_ENABLED=1 GOOS="$$($(GO) env GOHOSTOS)" GOARCH="$$($(GO) env GOHOSTARCH)" $(GO) test -mod=readonly -tags=nolibopusfile -race -count=1 -timeout=60s ./...

vet:
	$(host-check)
	CGO_ENABLED=1 GOOS="$$($(GO) env GOHOSTOS)" GOARCH="$$($(GO) env GOHOSTARCH)" $(GO) vet -mod=readonly -tags=nolibopusfile ./...

check:
	@files="$$("$$($(GO) env GOROOT)/bin/gofmt" -l cmd internal)"; test -z "$$files" || { printf 'Run make fmt for:\n%s\n' "$$files" >&2; exit 1; }
	sh -n tools/build-arm.sh
	for f in package/install.sh package/station/*.sh package/station/S99station-xiaozhi; do sh -n "$$f" || exit 1; done
	PYTHONDONTWRITEBYTECODE=1 $(PYTHON) -m unittest discover -s tools -p 'test_*.py'
	$(MAKE) test vet

build:
	sh tools/build-arm.sh build

release: build
	$(PYTHON) tools/release.py

clean:
	rm -rf build dist

install: release
	sh $(RELEASE)/install.sh "$(STATION_IP)"

status start stop bind:
	sh $(RELEASE)/install.sh "$(STATION_IP)" $@

uninstall:
	sh $(RELEASE)/install.sh "$(STATION_IP)" --uninstall

logs:
	MSYS_NO_PATHCONV=1 $(ADB) -s "$(STATION_IP):5555" shell tail -n 100 -f /data/station-xiaozhi/client.log

shell:
	$(ADB) -s "$(STATION_IP):5555" shell
