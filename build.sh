#!/usr/bin/env bash

set -x

echo "oliver"

export CGO_ENABLED=0
export GO_LDFLAGS="-extldflags \"-static\" -X main.VERSION=$CIRCLE_TAG -X main.COMMIT_SHA1=$CIRCLE_SHA1 -X main.BUILD_DATE=$(date +%F-%T)"

go get github.com/mitchellh/gox
go get github.com/tcnksm/ghr

gox --osarch="linux/386"   -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter"

if [ ! -f dist/redis_exporter ]; then
    echo "binary not found!"
    exit 1
fi

echo "Build Docker images"

docker version

echo "docker login"
docker login -e $DOCKER_EMAIL -u $DOCKER_USER -p $DOCKER_PASS
docker info
echo "docker login done"

docker build --rm=false -t "21zoo/redis_exporter:$CIRCLE_TAG" .
docker build --rm=false -t "21zoo/redis_exporter:latest" .

docker push "21zoo/redis_exporter:latest"
docker push "21zoo/redis_exporter:$CIRCLE_TAG"

docker build --rm=false -t "oliver006/redis_exporter:$CIRCLE_TAG" .
docker build --rm=false -t "oliver006/redis_exporter:latest" .
docker push "oliver006/redis_exporter:latest"
docker push "oliver006/redis_exporter:$CIRCLE_TAG"



echo "Building binaries"
echo ""
echo "GO_LDFLAGS: $GO_LDFLAGS"

gox -rebuild --osarch="darwin/amd64"  -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && tar -cvzf redis_exporter-$CIRCLE_TAG.darwin-amd64.tar.gz redis_exporter && rm redis_exporter && cd ..
gox -rebuild --osarch="darwin/386"    -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && tar -cvzf redis_exporter-$CIRCLE_TAG.darwin-386.tar.gz   redis_exporter && rm redis_exporter && cd ..
gox -rebuild --osarch="linux/amd64"   -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && tar -cvzf redis_exporter-$CIRCLE_TAG.linux-amd64.tar.gz  redis_exporter && rm redis_exporter && cd ..
gox -rebuild --osarch="linux/386"     -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && tar -cvzf redis_exporter-$CIRCLE_TAG.linux-386.tar.gz    redis_exporter && rm redis_exporter && cd ..
gox -rebuild --osarch="netbsd/amd64"  -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && tar -cvzf redis_exporter-$CIRCLE_TAG.netbsd-amd64.tar.gz redis_exporter && rm redis_exporter && cd ..
gox -rebuild --osarch="netbsd/386"    -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && tar -cvzf redis_exporter-$CIRCLE_TAG.netbsd-386.tar.gz   redis_exporter && rm redis_exporter && cd ..
gox -rebuild --osarch="windows/amd64" -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && zip -9    redis_exporter-$CIRCLE_TAG.windows-amd64.zip   redis_exporter.exe && rm redis_exporter.exe && cd ..
gox -rebuild --osarch="windows/386"   -ldflags "$GO_LDFLAGS" -output "dist/redis_exporter" && cd dist && zip -9    redis_exporter-$CIRCLE_TAG.windows-386.zip     redis_exporter.exe && rm redis_exporter.exe && cd ..

echo "Upload to Github"
ghr -t $GITHUB_TOKEN -u $CIRCLE_PROJECT_USERNAME -r $CIRCLE_PROJECT_REPONAME --replace $CIRCLE_TAG dist/

echo "Done"
