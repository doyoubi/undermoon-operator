#!/bin/sh
# This file will be copied and used in the `readinessProbe` of redis container.
# Please develop it first here and use `shellcheck` to check this script
# before moving it to the codes in `./controllers/storage.go`.
#
# As it's run with `sh -c`,
# the arguments starts from zero.
# Also all command should end with ';'.
#
# Usage:
# sh -c "$(cat ./scripts/redis-readiness.sh)" redis-ready 6379 5000

set +e;
TAG_FILE="${0}";
PORT="${1}";
OFFSET_THRESHOLD="${2}";

tag_file_dir=$(dirname "${TAG_FILE}");
mkdir -p "${tag_file_dir}";
if test -f "${TAG_FILE}"; then
    echo "${TAG_FILE} exists";
    exit 0;
fi;

repl_info=$(redis-cli -h localhost -p "${PORT}" INFO REPLICATION);
role=$(echo "${repl_info}" | grep 'role:' | cut -d':' -f2 | tr -d '\r' );
echo "role: ${role}";

if [ "${role}" = 'master' ]; then
    echo "role: ${role}. Create tag file ${TAG_FILE}";
    touch "${TAG_FILE}";
    exit 0;
fi;

slave_repl_offset=$(echo "${repl_info}" | grep 'master_repl_offset:' | cut -d':' -f2 | tr -d '\r');
if [ "${slave_repl_offset}" -eq 0 ]; then
    echo "Zero slave_repl_offset. The replica still cannot connect to its master.";
    exit 1;
fi;

master_host=$(echo "${repl_info}" | grep 'master_host:' | cut -d':' -f2 | tr -d '\r');
master_port=$(echo "${repl_info}" | grep 'master_port:' | cut -d':' -f2 | tr -d '\r');
echo "master: ${master_host} ${master_port}";

if [ "${master_port}" -eq 0 ]; then
    echo "Zero master port. The role is not set yet.";
    exit 1;
fi;

master_repl_info=$(redis-cli -h "${master_host}" -p "${master_port}" INFO REPLICATION);
master_repl_offset=$(echo "${master_repl_info}" | grep 'master_repl_offset:' | cut -d':' -f2 | tr -d '\r');
echo "master_repl_offset: ${master_repl_offset} slave_repl_offset: ${slave_repl_offset}";
offset=$((master_repl_offset - slave_repl_offset));
echo "offset: ${offset}";

if [ "${master_repl_offset}" -gt 0 ] && [ "${offset}" -ge 0 ] && [ "${offset}" -lt "${OFFSET_THRESHOLD}" ]; then
    echo "Replication is done. Create tag file ${TAG_FILE}";
    touch "${TAG_FILE}";
    exit 0;
fi;

echo "replica pending on replication";
exit 1;
