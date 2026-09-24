# mterm: build, test e controlli della documentazione.
#
#   make             ./mterm, il binario statico Linux x86-64 registrato nel repository
#   make test        controlli dei manuali (sull'eseguibile registrato), poi ricostruisce ./mterm, go vet, gofmt, test end-to-end
#   make e2e         solo i test end-to-end, sull'eseguibile ./mterm così com'è (non serve Go)
#   make docs-check  solo i controlli dei manuali
#   make dist        binari statici per Linux amd64 e arm64 in dist/
#   make install     copia ./mterm in ~/.local/bin (PREFIX lo cambia)
#   make clean       elimina dist/ (./mterm resta: fa parte del repository)

# Go: quello nel PATH, altrimenti l'installazione in home (~/.local/go).
GO      ?= $(shell command -v go 2>/dev/null || echo $(HOME)/.local/go/bin/go)
LDFLAGS := -s -w
PREFIX  ?= $(HOME)/.local
BINDIR  ?= $(PREFIX)/bin

# CGO disattivato => binario davvero statico e autocontenuto.
export CGO_ENABLED = 0

# I test end-to-end girano isolati: socket e log in una cartella propria.
E2E_XRT ?= /tmp/mterm-test-xrt
E2E     := $(sort $(wildcard tests/e2e*.py))

.PHONY: all build vet fmt-check docs-check e2e test dist install uninstall clean

all: build

build:
	GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o mterm .
	@echo "fatto: ./mterm ($$(du -h mterm | cut -f1)), statico"

vet:
	$(GO) vet ./...

fmt-check:
	@out=$$($$(dirname $(GO))/gofmt -l .); \
	if [ -n "$$out" ]; then echo "file da formattare con gofmt:"; echo "$$out"; exit 1; fi

docs-check:
	python3 tools/check-docs.py

# Test end-to-end: guidano ./mterm attraverso una pty reale (servono Python 3 e /bin/sh).
e2e:
	@mkdir -p $(E2E_XRT)
	@for t in $(E2E); do \
	  echo "== $$t"; \
	  XDG_RUNTIME_DIR=$(E2E_XRT) python3 $$t || exit 1; \
	done
	@echo "test end-to-end: tutti superati ($(words $(E2E)) file)"

test: docs-check build vet fmt-check e2e

dist:
	@mkdir -p dist
	for arch in amd64 arm64; do \
	  GOOS=linux GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
	    -o dist/mterm-linux-$$arch . || exit 1; \
	done
	cd dist && sha256sum mterm-linux-* > SHA256SUMS && cat SHA256SUMS

install:
	@[ -x mterm ] || $(MAKE) build
	@mkdir -p $(BINDIR)
	cp mterm $(BINDIR)/mterm
	@echo "installato in $(BINDIR)/mterm"

uninstall:
	rm -f $(BINDIR)/mterm

clean:
	rm -rf dist
