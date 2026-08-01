GO ?= go
CORPUS_DIR ?= testdata/corpus/py-pdf

# Pinned so corpus results are reproducible. Bump deliberately, then
# regenerate the manifest and review the diff.
CORPUS_REPO ?= https://github.com/py-pdf/sample-files.git
CORPUS_REF  ?= 818dc013ad1f537198e9fcfae8a6b0dffe25ffa3

.PHONY: help
help:
	@echo "decant development targets:"
	@echo "  make test        run the test suite"
	@echo "  make check       gofmt, vet, staticcheck, race tests"
	@echo "  make corpus      fetch the py-pdf sample-files corpus"
	@echo "  make corpus-test run the corpus tests"
	@echo "  make manifest    regenerate the corpus golden manifest"
	@echo "  make fuzz        run every fuzz target briefly"
	@echo "  make clean       remove build output and the fetched corpus"

.PHONY: build
build:
	$(GO) build -o decant ./cmd/decant

.PHONY: test
test:
	$(GO) test ./...

.PHONY: check
check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt-clean:"; echo "$$unformatted"; exit 1; \
	fi
	$(GO) vet ./...
	staticcheck ./...
	$(GO) test -race ./...

# The corpus is CC-BY-SA-4.0. decant ships MIT, and spec section 4.6 rules out
# vendoring share-alike material, so it is fetched on demand and gitignored
# rather than committed. Corpus tests skip when it is absent.
.PHONY: corpus
corpus:
	@if [ -d "$(CORPUS_DIR)/.git" ]; then \
		echo "corpus already present at $(CORPUS_DIR)"; \
	else \
		mkdir -p $(dir $(CORPUS_DIR)); \
		git clone --quiet $(CORPUS_REPO) $(CORPUS_DIR); \
	fi
	@cd $(CORPUS_DIR) && git fetch --quiet origin && git checkout --quiet $(CORPUS_REF) 2>/dev/null \
		|| echo "warning: could not check out $(CORPUS_REF); using the default branch"
	@echo "corpus ready: $$(find $(CORPUS_DIR) -name '*.pdf' | wc -l | tr -d ' ') PDFs"

.PHONY: corpus-test
corpus-test: corpus
	$(GO) test -run TestCorpus -v ./...

.PHONY: manifest
manifest: corpus
	$(GO) test -run TestCorpusManifest -update ./...
	@echo "manifest regenerated; review the diff before committing"

.PHONY: fuzz
fuzz:
	$(GO) test -run XXX -fuzz FuzzLexer     -fuzztime 30s ./internal/pdf/
	$(GO) test -run XXX -fuzz FuzzInterpret -fuzztime 30s ./internal/pdf/
	$(GO) test -run XXX -fuzz FuzzOpen      -fuzztime 30s ./internal/pdf/
	$(GO) test -run XXX -fuzz FuzzParseCMap -fuzztime 30s ./internal/pdf/

.PHONY: clean
clean:
	rm -f decant
	rm -rf testdata/corpus/py-pdf
