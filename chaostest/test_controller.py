import sys
import time
import json
import random
import signal
import asyncio
import itertools

from loguru import logger


UNDERMOON_RESOURCE = "undermoon"


async def keep_randomly_scaling(undermoon_cluster_name):
    while True:
        chunk_number = random.randint(1, 8)
        await randomly_scale(undermoon_cluster_name, chunk_number)

        sleep_time = int(abs(random.gauss(0, 60) + 40))
        await asyncio.sleep(sleep_time)


async def randomly_scale(undermoon_cluster_name, chunk_number):
    path = (
        '[{"op": "replace", "path": "/spec/containers/0/image", "value":"new image"}]'
    )
    path = [{"op": "replace", "path": "/spec/chunkNumber", "value": chunk_number,}]
    cmd = [
        "kubectl",
        "patch",
        UNDERMOON_RESOURCE,
        undermoon_cluster_name,
        "--type='json'",
        "-p='{}'".format(json.dumps(path)),
    ]

    logger.info("scale to {}".format(chunk_number))

    proc = await asyncio.create_subprocess_shell(
        " ".join(cmd), stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE
    )
    stdout, stderr = await proc.communicate()

    if proc.returncode != 0:
        logger.error("failed to change chunk number: {}", stderr.decode())
    s = stdout.decode()
    if s:
        logger.info(s)
    s = stderr.decode()
    if s:
        logger.info(s)


async def keep_randomly_killing(undermoon_cluster_name):
    while True:
        await randomly_kill(undermoon_cluster_name)
        sleep_time = int(abs(random.gauss(0, 80) + 40))
        await asyncio.sleep(sleep_time)


async def randomly_kill(undermoon_cluster_name):
    pods = await get_undermoon_pods(undermoon_cluster_name)
    if not pods:
        return

    killed_pod_name = random.choice(pods)
    logger.info("killing {}".format(killed_pod_name))
    cmd = ["kubectl", "delete", "pod", killed_pod_name]

    proc = await asyncio.create_subprocess_shell(
        " ".join(cmd), stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE
    )
    stdout, stderr = await proc.communicate()

    if proc.returncode != 0:
        logger.error("failed to kill pod {}: {}", killed_pod_name, stderr.decode())
    s = stdout.decode()
    if s:
        logger.info(s)
    s = stderr.decode()
    if s:
        logger.info(s)


async def get_undermoon_pods(undermoon_cluster_name):
    cmd = ["kubectl", "get", "pods"]
    proc = await asyncio.create_subprocess_shell(
        " ".join(cmd), stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE
    )
    stdout, stderr = await proc.communicate()

    if proc.returncode != 0:
        logger.error("failed to get pods: {}".format(stderr.decode()))
        return []

    s = stdout.decode()
    lines = s.split("\n")
    pods = []
    for line in lines:
        if not line:
            continue
        pod_name = line.split(" ")[0]
        if undermoon_cluster_name not in pod_name:
            continue
        pods.append(pod_name)
    return pods


async def main(undermoon_cluster_name, enable_killing):
    futs = [
        keep_randomly_scaling(undermoon_cluster_name),
    ]
    if enable_killing:
        futs.append(keep_randomly_killing(undermoon_cluster_name))
    await asyncio.gather(*futs)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        raise Exception("Missing undermoon cluster name or killing flag")
    undermoon_cluster_name = sys.argv[1]
    enable_killing = sys.argv[2] == "enable-killing"
    print("undermoon cluster name:", undermoon_cluster_name)
    print("enable killing: ", enable_killing)
    asyncio.run(main(undermoon_cluster_name, enable_killing))
