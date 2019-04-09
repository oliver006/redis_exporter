#!/usr/bin/env bash

set -u -e -o pipefail

if [[ -z "${DRONE_TAG}" ]] ; then
  echo 'ERROR: Missing DRONE_TAG env'
  exit 1
fi

echo "Building binaries for Github"
echo ""
export CGO_ENABLED=0
export GO_LDFLAGS="-s -w -extldflags \"-static\" -X main.VERSION=$DRONE_TAG -X main.COMMIT_SHA1=$DRONE_COMMIT_SHA -X main.BUILD_DATE=$(date +%F-%T)"
echo  "GO_LDFLAGS: $GO_LDFLAGS"

go get github.com/mitchellh/gox
go get github.com/tcnksm/ghr
if [[ -f 'go.mod' ]] ; then
  go mod tidy
fi

gox -verbose -arch="386 amd64" -rebuild -ldflags "${GO_LDFLAGS}" -output '.build/{{.OS}}-{{.Arch}}/{{.Dir}}'

mkdir -p dist
for build in $(ls .build); do
  echo "Creating archive for ${build}"
  if [[ "${build}" =~ ^windows-.*$ ]] ; then
    # Make sure to clear out zip files to prevent zip from appending to the archive.
    rm "dist/redis_exporter-${DRONE_TAG}.${build}.zip" || true
    cd ".build/${build}" && zip --quiet -9 "../../dist/redis_exporter-${DRONE_TAG}.${build}.zip" 'redis_exporter.exe' && cd ../../
  else
    tar -C ".build/${build}" -czf "dist/redis_exporter-${DRONE_TAG}.${build}.tar.gz" 'redis_exporter'
  fi
done

echo "Upload to Github"

cd dist
sha256sum *.gz *.zip > sha256sums.txt
cd ..

ghr -u oliver006 -r redis_exporter --replace "${DRONE_TAG}" dist/

echo "Done"
