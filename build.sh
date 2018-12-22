#!/usr/bin/env bash

echo "Build Docker images"

docker version

echo "docker login"
docker login -u $DOCKER_USER -p $DOCKER_PASS
docker info
echo "docker login done"

export BUILD_ARGS="--rm=false --build-arg TAG=${CIRCLE_TAG} --build-arg SHA1=${CIRCLE_SHA1} --build-arg DATE=$(date +%F-%T)"
echo  "BUILD_ARGS: $BUILD_ARGS"

docker build -t "21zoo/redis_exporter:$CIRCLE_TAG" $BUILD_ARGS .
docker push     "21zoo/redis_exporter:$CIRCLE_TAG"

docker build -t "21zoo/redis_exporter:latest" $BUILD_ARGS .
docker push     "21zoo/redis_exporter:latest"

docker build -t "oliver006/redis_exporter:$CIRCLE_TAG" $BUILD_ARGS  .
docker push     "oliver006/redis_exporter:$CIRCLE_TAG"

docker build -t "oliver006/redis_exporter:latest" $BUILD_ARGS  .
docker push     "oliver006/redis_exporter:latest"



echo "Building binaries for Github"
echo ""
export CGO_ENABLED=0
export GO_LDFLAGS="-s -w -extldflags \"-static\" -X main.VERSION=$CIRCLE_TAG -X main.COMMIT_SHA1=$CIRCLE_SHA1 -X main.BUILD_DATE=$(date +%F-%T)"
echo  "GO_LDFLAGS: $GO_LDFLAGS"

go get github.com/mitchellh/gox
go get github.com/tcnksm/ghr

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
