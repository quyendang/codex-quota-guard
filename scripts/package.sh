#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-}"
if [[ -z "${VERSION}" ]]; then
  echo "usage: ./scripts/package.sh <version>" >&2
  exit 2
fi
VERSION="${VERSION#v}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

GOOS_VALUE="$(go env GOOS)"
GOARCH_VALUE="$(go env GOARCH)"
case "${GOOS_VALUE}" in
  darwin) EXT="dylib" ;;
  linux) EXT="so" ;;
  windows) EXT="dll" ;;
  *) echo "unsupported GOOS ${GOOS_VALUE}" >&2; exit 2 ;;
esac

NAME="codex-quota-guard"
DIST="${ROOT}/dist"
WORK="${DIST}/${NAME}_${VERSION}_${GOOS_VALUE}_${GOARCH_VALUE}"
mkdir -p "${DIST}"
rm -rf "${WORK}"
mkdir -p "${WORK}"

LIB="${WORK}/${NAME}.${EXT}"
CGO_ENABLED=1 go build -buildmode=c-shared -o "${LIB}" .
rm -f "${WORK}/${NAME}.h"

ARCHIVE="${DIST}/${NAME}_${VERSION}_${GOOS_VALUE}_${GOARCH_VALUE}.zip"
rm -f "${ARCHIVE}"
(
  cd "${WORK}"
  zip -q -9 "${ARCHIVE}" "${NAME}.${EXT}"
)

(
  cd "${DIST}"
  shasum -a 256 "$(basename "${ARCHIVE}")" >> checksums.txt
)

echo "${ARCHIVE}"
