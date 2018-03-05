#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail
#set -o xtrace

[[ ${ARGS:-} ]] || ARGS=""

/bin/redis_exporter $ARGS