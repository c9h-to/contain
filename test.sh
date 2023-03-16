#!/usr/bin/env bash
[ -z "$DEBUG" ] || set -x
set -eo pipefail

go test ./pkg/...

DOCKER=docker
REGISTRY_PORT=22500
REGISTRY_NAME=contain-test-registry

$DOCKER inspect $REGISTRY_NAME 2>/dev/null >/dev/null ||
  $DOCKER run --rm -d -p 22500:5000 --name $REGISTRY_NAME registry:2

mkdir -p dist
go build -ldflags="-X main.BUILD=test-$(uname -m)" -o dist/contain-test cmd/contain/main.go

./dist/contain-test --version
./dist/contain-test --help

skaffold --default-repo=localhost:22500 -f skaffold.test.yaml build --file-output=dist/test.artifacts

skaffold -f skaffold.test.yaml test -a dist/test.artifacts

# test hacks for things that container-structure-test doesn't (?) support
localtest1=$(cat dist/test.artifacts | jq -r '.builds | .[] | select(.imageName=="localdir1") | .tag')
localtest1_amd64=$(crane --platform=linux/amd64 digest $localtest1)
localtest1_arm64=$(crane --platform=linux/arm64 digest $localtest1)
[ -z "$localtest1_amd64" ] && echo "amd64 architecture missing for $localtest1" && exit 1
[ -z "$localtest1_arm64" ] && echo "arm64 architecture missing for $localtest1" && exit 1
[ "$localtest1_amd64" != "$localtest1_arm64" ] && echo "warning: amd64 != arm64 ($localtest1_amd64 != $localtest1_arm64)" || echo "ok: $localtest1 is multi-arch"

localtest1_base=$(crane manifest $localtest1 | jq -r '.annotations."org.opencontainers.image.base.name"')
[ "$localtest1_base" = "index.docker.io/library/busybox:latest" ] || (echo "unexpected annotations $localtest1_base" && exit 1)

for F in $(find test -name skaffold.fail-\*.yaml); do
  echo "=> Fail test: $F ..."
  RESULT=0
  skaffold --default-repo=localhost:22500 -f $F build > $F.out 2>&1 || RESULT=$?
  [ $RESULT -eq 0 ] && echo "Expected build failure with $F, got exit $RESULT after:" && cat $F.out && exit 1
  echo "ok"
done

# $DOCKER stop $REGISTRY_NAME
