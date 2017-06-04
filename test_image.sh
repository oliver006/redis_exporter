#!/bin/bash
set -exo pipefail
docker_image=$1
port=$2

wait_start() {
    for in in {1..10}; do
    if  /usr/bin/curl -s -m 5 -f "http://localhost:${port}/metrics" > /dev/null ; then
      echo " >redis-exporter working well"
      return 0 ;
    else
      sleep 1
    fi
    done

  echo " > redis-exporter not working. Run `docker logs ${docker_image}`"
  return 1

}

docker_start() {
    # Start redis-server
    docker run -d -p 6379:6379 --name redis-server redis > /dev/null
    docker run -d -p "${port}":"${port}" --name redis_exporter "${docker_image}" > /dev/null
}

docker_stop() {
    # Stop redis-server
    docker rm --force $(docker ps -aqf "name=redis-server") $(docker ps -aqf "name=redis_exporter") > /dev/null
}

if [[ "$#" -ne 2 ]] ; then
    echo "Usage: $0 quay.io/oliver006/redis_exporter:v0.12.0 9121" >&2
    exit 1
fi

docker_start
wait_start
docker_stop
