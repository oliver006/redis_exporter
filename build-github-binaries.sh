#!/usr/bin/env sh

echo "Building binaries for Github"
echo ""
export CGO_ENABLED=0
export GO_LDFLAGS="-s -w -extldflags \"-static\" -X main.VERSION=$DRONE_TAG -X main.COMMIT_SHA1=$DRONE_COMMIT_SHA -X main.BUILD_DATE=$(date +%F-%T)"
echo  "GO_LDFLAGS: $GO_LDFLAGS"

go get github.com/mitchellh/gox
go get github.com/tcnksm/ghr

gox -rebuild --osarch="darwin/amd64"  -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && tar -cvzf redis_exporter-$DRONE_TAG.darwin-amd64.tar.gz redis_exporter && rm redis_exporter && cd ..
gox -rebuild --osarch="darwin/386"    -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && tar -cvzf redis_exporter-$DRONE_TAG.darwin-386.tar.gz   redis_exporter && rm redis_exporter && cd ..
gox -rebuild --osarch="linux/amd64"   -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && tar -cvzf redis_exporter-$DRONE_TAG.linux-amd64.tar.gz  redis_exporter && rm redis_exporter && cd ..
gox -rebuild --osarch="linux/386"     -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && tar -cvzf redis_exporter-$DRONE_TAG.linux-386.tar.gz    redis_exporter && rm redis_exporter && cd ..
gox -rebuild --osarch="netbsd/amd64"  -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && tar -cvzf redis_exporter-$DRONE_TAG.netbsd-amd64.tar.gz redis_exporter && rm redis_exporter && cd ..
gox -rebuild --osarch="netbsd/386"    -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && tar -cvzf redis_exporter-$DRONE_TAG.netbsd-386.tar.gz   redis_exporter && rm redis_exporter && cd ..
gox -rebuild --osarch="windows/amd64" -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && zip -9    redis_exporter-$DRONE_TAG.windows-amd64.zip   redis_exporter.exe && rm redis_exporter.exe && cd ..
gox -rebuild --osarch="windows/386"   -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && zip -9    redis_exporter-$DRONE_TAG.windows-386.zip     redis_exporter.exe && rm redis_exporter.exe && cd ..

echo "Upload to Github"

pwd
ls -la 
ls -la dist/

ghr -u oliver006 -r redis_exporter --replace $DRONE_TAG dist/

echo "Done"
