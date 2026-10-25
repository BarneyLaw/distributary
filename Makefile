# Everyday commands. Run from the repo root on Linux (our Ubuntu VM).
#
#   make build           binary for this machine, used by solution_server.py
#   make bins            both committed binaries (run before pushing server changes)
#   make test            go vet + unit tests with the race detector
#   make check           the professor's submission_check.py
#   make verify-harness  prove the professor's files are unmodified
#   make t1 ... make t4  public tests T1-T4, 3 runs each, results in experiments/results/
#   make py-baseline     the original Python starter server, same harness, for comparison

GO      ?= go
PY      ?= python3
PKG     := ./cmd/distributary
BIN     := bin
ARCH    := $(shell $(GO) env GOARCH)
GOBUILD := CGO_ENABLED=0 GOOS=linux $(GO) build -trimpath -ldflags '-s -w'
RESULTS := ../experiments/results

.PHONY: build bins vet test check verify-harness t1 t2 t3 t4 py-baseline clean

build:
	$(GOBUILD) -o $(BIN)/distributary-linux-$(ARCH) $(PKG)

bins:
	GOARCH=amd64 $(GOBUILD) -o $(BIN)/distributary-linux-amd64 $(PKG)
	GOARCH=arm64 $(GOBUILD) -o $(BIN)/distributary-linux-arm64 $(PKG)

vet:
	$(GO) vet ./...

test: vet
	$(GO) test -race ./...

check: build
	$(PY) harness/submission_check.py

verify-harness:
	cd harness && sha256sum -c --quiet SHA256SUMS && echo "harness files unmodified"

# run_demo.py writes its CSV/JSON beside itself, so --output-stem points back
# out to experiments/results/.
t1: build
	cd harness && $(PY) run_demo.py --clients 20 --duration 20 --runs 3 --quiet-server --output-stem $(RESULTS)/t1

t2: build
	cd harness && $(PY) run_demo.py --clients 100 --duration 40 --runs 3 --quiet-server --output-stem $(RESULTS)/t2

t3: build
	cd harness && $(PY) run_demo.py --clients 100 --duration 60 --runs 3 --quiet-server \
		--slow-percent 10 --slow-ms 500 --output-stem $(RESULTS)/t3

t4: build
	cd harness && $(PY) run_demo.py --clients 100 --duration 60 --runs 3 --quiet-server \
		--disconnect-percent 20 --disconnect-at 15 --disconnect-seconds 5 --output-stem $(RESULTS)/t4

# Runs the professor's original Python server through the same harness, in a
# scratch copy so nothing in harness/ changes. Override ARGS for other tests:
#   make py-baseline ARGS="--clients 100 --duration 40 --runs 3"
ARGS ?= --clients 100 --duration 60 --runs 3 --slow-percent 10 --slow-ms 500
py-baseline:
	rm -rf .baseline && mkdir .baseline
	cp harness/analyse_results.py harness/caption_source.py harness/load_clients.py harness/run_demo.py .baseline/
	cp harness/starter_server.py .baseline/solution_server.py
	cd .baseline && $(PY) run_demo.py $(ARGS) --quiet-server --output-stem py_baseline

clean:
	rm -rf .baseline $(BIN)/distributary-linux-*