#!/bin/bash

# Generate test certificates:
#
#   ca.{crt,key}          Self signed CA certificate.
#   redis.{crt,key}       A certificate with no key usage/policy restrictions.

dir=`dirname $0`

# Generate CA
openssl genrsa -out ${dir}/ca.key 4096
openssl req \
    -x509 -new -nodes -sha256 \
    -key ${dir}/ca.key \
    -days 3650 \
    -subj '/O=redis_exporter/CN=Certificate Authority' \
    -out ${dir}/ca.crt

# Generate cert
openssl genrsa -out ${dir}/redis.key 2048
openssl req \
    -new -sha256 \
    -subj "/O=redis_exporter/CN=localhost" \
    -key ${dir}/redis.key | \
    openssl x509 \
        -req -sha256 \
        -CA ${dir}/ca.crt \
        -CAkey ${dir}/ca.key \
        -CAserial ${dir}/ca.txt \
        -CAcreateserial \
        -days 3650 \
        -out ${dir}/redis.crt
