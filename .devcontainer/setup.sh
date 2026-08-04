#!/usr/bin/env bash
# Tools the test suite needs but does not require.
#
# This is the point of the devcontainer. Without epubcheck and poppler the
# corpus tests skip rather than fail, so a contributor without them sees a
# green suite and reasonably concludes their change is tested. It is not:
# epubcheck validation and the pdftotext reading-order comparison are both
# CI gates that were simply absent from their run.
set -euo pipefail

echo "installing test dependencies"
for i in 1 2 3; do
  if timeout 5m sudo apt-get update \
     && timeout 5m sudo apt-get install -y --no-install-recommends epubcheck poppler-utils; then
    break
  fi
  echo "apt attempt $i failed; retrying"
  [ "$i" -eq 3 ] && { echo "could not install epubcheck and poppler"; exit 1; }
  sleep $((i * 10))
done

echo "installing staticcheck"
go install honnef.co/go/tools/cmd/staticcheck@latest

echo
echo "verifying the toolchain:"
printf '  go          %s\n' "$(go version | awk '{print $3}')"
printf '  staticcheck %s\n' "$($(go env GOPATH)/bin/staticcheck --version 2>&1 | awk '{print $NF}')"
printf '  epubcheck   %s\n' "$(epubcheck --version 2>&1 | head -1 || echo 'MISSING')"
printf '  pdftotext   %s\n' "$(pdftotext -v 2>&1 | head -1 || echo 'MISSING')"

cat <<'MSG'

Ready. The corpus is not vendored (it is CC-BY-SA-4.0 and this repo is MIT):

  make corpus     fetch it, once
  go test ./...   full suite; corpus tests skip until you do

MSG
