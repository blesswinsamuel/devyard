#!/usr/bin/env bash
# Run the e2e suite on Linux from any host with Docker, in a single
# golang container. Isolation comes from the harness sandbox, not the
# container; the container only provides the OS.
#
#   scripts/e2e-linux.sh                                # go test -count=1 ./test/e2e/...
#   scripts/e2e-linux.sh -race -run TestLedger_S2 ./test/e2e/api/
#   DEVYARD_E2E_CHAOS=1 scripts/e2e-linux.sh ./test/e2e/chaos/
#
# Arguments are passed to `go test`. Build and module caches persist in
# named volumes (devyard-e2e-gocache, devyard-e2e-gomod).
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
image="${DEVYARD_E2E_IMAGE:-golang:1.26}"

if [ "$#" -eq 0 ]; then
	set -- -count=1 ./test/e2e/...
fi

tty_flags=()
if [ -t 0 ] && [ -t 1 ]; then
	tty_flags=(-it)
fi

# --init reaps orphans: daemons and runners are re-parented to pid 1, and
# without a reaper their zombies would look alive to the leak checker.
exec docker run --rm --init ${tty_flags[@]+"${tty_flags[@]}"} \
	-v "$repo:/src" \
	-w /src \
	-v devyard-e2e-gocache:/root/.cache/go-build \
	-v devyard-e2e-gomod:/go/pkg/mod \
	-e GOTOOLCHAIN=auto \
	-e GOFLAGS \
	-e DEVYARD_E2E_CHAOS \
	-e DEVYARD_E2E_CHAOS_SEED \
	-e DEVYARD_E2E_CHAOS_STEPS \
	-e DEVYARD_E2E_TIMEOUT_SCALE \
	-e DEVYARD_E2E_KEEP \
	"$image" \
	bash -euc 'git config --global --add safe.directory /src; exec go test "$@"' go-test "$@"
