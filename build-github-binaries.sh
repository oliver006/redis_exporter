#!/usr/bin/env bash


set -u -e -o pipefail

if [[ -z "${DRONE_TAG}" ]] ; then
  echo 'ERROR: Missing DRONE_TAG env'
  exit 1
fi

echo "Building binaries for Github"
echo ""
export CGO_ENABLED=0
export GO_LDFLAGS="-s -w -extldflags \"-static\" -X main.BuildVersion=$DRONE_TAG -X main.BuildCommitSha=$DRONE_COMMIT_SHA -X main.BuildDate=$(date +%F-%T)"
echo  "GO_LDFLAGS: $GO_LDFLAGS \n"

echo "go get gox & ghr"
go get github.com/mitchellh/gox
go get github.com/tcnksm/ghr
if [[ -f 'go.mod' ]] ; then
  go mod tidy
fi

gox -verbose -os="darwin linux windows freebsd netbsd openbsd" -arch="386 amd64" -rebuild -ldflags "${GO_LDFLAGS}" -output ".build/redis_exporter-${DRONE_TAG}.{{.OS}}-{{.Arch}}/{{.Dir}}"
gox -verbose -os="linux freebsd netbsd" -arch="arm" -rebuild -ldflags "${GO_LDFLAGS}" -output ".build/redis_exporter-${DRONE_TAG}.{{.OS}}-{{.Arch}}/{{.Dir}}"
gox -verbose -os="linux" -arch="arm64 mips64 mips64le ppc64 ppc64le s390x" -rebuild -ldflags "${GO_LDFLAGS}" -output ".build/redis_exporter-${DRONE_TAG}.{{.OS}}-{{.Arch}}/{{.Dir}}"

mkdir -p dist

for build in $(ls .build); do
  echo "Creating archive for ${build}"

  cp LICENSE README.md ".build/${build}/"

  if [[ "${build}" =~ windows-.*$ ]] ; then

    # Make sure to clear out zip files to prevent zip from appending to the archive.
    rm "dist/${build}.zip" || true
    cd ".build/" && zip -r --quiet -9 "../dist/${build}.zip" "${build}" && cd ../
  else
    tar -C ".build/" -czf "dist/${build}.tar.gz" "${build}"
  fi
done

cd dist
sha256sum *.gz *.zip > sha256sums.txt
ls -la
cd ..

echo "Upload to Github"
ghr  -parallel 1 -u oliver006 -r redis_exporter --replace "${DRONE_TAG}" dist/

echo "Done"
