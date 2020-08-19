#!/bin/sh
kind delete cluster
registry_container_id=$(docker ps -a | grep kind-registry | awk '{print $1}')
if [ -n "${registry_container_id}" ]; then
    docker stop ${registry_container_id} || true
    docker rm ${registry_container_id} || true
fi

