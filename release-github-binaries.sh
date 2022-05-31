#!/usr/bin/env bash


set -u -e -o pipefail

if [[ -z "${DRONE_TAG}" ]] ; then
  echo 'ERROR: Missing DRONE_TAG env'
  exit 1
fi

echo "go install ghr"
go install github.com/tcnksm/ghr@v0.14.0

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
