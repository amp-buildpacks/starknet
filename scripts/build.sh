#!/usr/bin/env bash
set -euo pipefail

GOMOD=$(head -1 go.mod | awk '{print $2}')
GOOS="linux" GOARCH="amd64" go build -ldflags='-s -w' -o linux/amd64/bin/main "$GOMOD/cmd/main"
GOOS="linux" GOARCH="arm64" go build -ldflags='-s -w' -o linux/arm64/bin/main "$GOMOD/cmd/main"

if [ "${STRIP:-false}" != "false" ]; then
  strip linux/amd64/bin/main linux/arm64/bin/main
fi

if [ "${COMPRESS:-none}" != "none" ]; then
  $COMPRESS linux/amd64/bin/main linux/arm64/bin/main
fi

ln -fs main linux/amd64/bin/build
ln -fs main linux/arm64/bin/build
ln -fs main linux/amd64/bin/detect
ln -fs main linux/arm64/bin/detect


GOOS="linux" go build -ldflags='-s -w' -o bin/main "$GOMOD/cmd/main"

if [ "${STRIP:-false}" != "false" ]; then
  strip bin/main
fi

if [ "${COMPRESS:-none}" != "none" ]; then
  $COMPRESS bin/main
fi

ln -fs main bin/build
ln -fs main bin/detect
