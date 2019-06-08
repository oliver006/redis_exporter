#!/usr/bin/env bash

# Build script to dev env or release sending to Github

set -u -e -o pipefail

export GO111MODULE=on
if [[ -f 'go.mod' ]] ; then
  go mod tidy
fi

export ARGCMD=${1:-'*'}

function build() {

  export BUILD_TAG=${BUILDTAG:-"latest"}
  export BUILD_COMMIT_SHA=$(git rev-parse HEAD)

  echo "Building binary"
  echo ""
  export CGO_ENABLED=0
  export GO_LDFLAGS="-s -w -extldflags \"-static\" -X main.BuildVersion=$BUILD_TAG -X main.BuildCommitSha=$BUILD_COMMIT_SHA -X main.BuildDate=$(date +%F-%T)"

  echo  "GO_LDFLAGS: $GO_LDFLAGS"
  test -d bin/ || mkdir -p bin
  go build -ldflags "${GO_LDFLAGS}" -o bin/redis_exporter

}

function buildAll() {

  if [[ -z "${DRONE_TAG}" ]] ; then
    echo 'ERROR: Missing DRONE_TAG env'
    exit 1
  fi

  echo "Building binaries for Github"
  echo ""
  export CGO_ENABLED=0
  export GO_LDFLAGS="-s -w -extldflags \"-static\" -X main.BuildVersion=$DRONE_TAG -X main.BuildCommitSha=$DRONE_COMMIT_SHA -X main.BuildDate=$(date +%F-%T)"
  echo  "GO_LDFLAGS: $GO_LDFLAGS"

  go get github.com/mitchellh/gox
  gox -verbose -os="darwin linux windows freebsd netbsd openbsd" -arch="386 amd64" -rebuild -ldflags "${GO_LDFLAGS}" -output ".build/redis_exporter-${DRONE_TAG}.{{.OS}}-{{.Arch}}/{{.Dir}}"

}

function upload() {

  go get github.com/tcnksm/ghr

  mkdir -p dist
  for build in $(ls .build); do
    echo "Creating archive for ${build}"

    cp LICENSE README.md ".build/${build}/"

    if [[ "${build}" =~ windows-.*$ ]] ; then

      # Make sure to clear out zip files to prevent zip from appending to the archive.
      rm "dist/redis_exporter-${DRONE_TAG}.${build}.zip" || true
      cd ".build/" && zip -r --quiet -9 "../dist/${build}.zip" "${build}" && cd ../
    else
      tar -C ".build/" -czf "dist/${build}.tar.gz" "${build}"
    fi
  done


  cd dist
  ls -la
  sha256sum *.gz *.zip > sha256sums.txt
  ls -la
  cd ..

  echo "Upload to Github"

  #ghr -u oliver006 -r redis_exporter --replace "${DRONE_TAG}" dist/

  echo "Done"

}

case $ARGCMD in
  "build") build;;
  "buildAll") buildAll;;
  "upload") upload;;
  "*") buildAll; upload;;
esac