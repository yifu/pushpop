#!/bin/sh
set -e

dir=$(git rev-parse --show-toplevel)
cd "$dir"

CGO_ENABLED=0 go build -o push ./cmd/push
CGO_ENABLED=0 go build -o pop ./cmd/pop
