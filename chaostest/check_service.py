import sys
import time
import random
import signal
import asyncio

from loguru import logger

from cluster_client import AioRedisClusterClient


class CheckerError(Exception):
    pass


class KeyValueChecker:
    MAX_KVS = 2000

    def __init__(self, checker_name, client):
        self.checker_name = checker_name
        self.client = client
        self.kvs = set()
        self.deleted_kvs = set()
        self.lua_keys = ['test:lua:{}:{{tag}}:{}:{}'.format(
            checker_name, int(time.time()), i) for i in range(10)]
        self.lua_value = None

    async def loop_check(self):
        while True:
            if len(self.kvs) >= self.MAX_KVS or len(self.deleted_kvs) >= self.MAX_KVS:
                await self.del_keys(self.kvs)
                return
            await self.checker_key_value()
            await asyncio.sleep(1)

    async def checker_key_value(self):
        try:
            n = random.randint(0, 10)
            if n < 4:
                await asyncio.gather(
                    self.check_set(), self.check_mset(), self.check_lua_mset(),
                )
            elif n < 8:
                await asyncio.gather(
                    self.check_get(), self.check_mget(), self.check_lua_mget(),
                )
            else:
                await self.check_del()
        except Exception as e:
            logger.error("REDIS_TEST_FAILED: {} {}", e, type(e))
            raise

    async def check_set(self):
        if len(self.kvs) >= self.MAX_KVS:
            return

        t = int(time.time())
        for i in range(10):
            k = "test:{}:{}:{}".format(self.checker_name, t, i)
            try:
                res, address = await self.client.set(k, k)
            except Exception as e:
                logger.error("REDIS_TEST: failed to set {}: {} {}", k, e, type(e))
                raise
            if not res:
                logger.info(
                    "REDIS_TEST: invalid response: {} address: {}", res, address
                )
                continue
            self.kvs.add(k)
            self.deleted_kvs.discard(k)

    async def check_mset(self):
        if len(self.kvs) >= self.MAX_KVS:
            return

        t = int(time.time())
        tag = "{" + str(random.randint(0, 2)) + "}"
        keys = [
            "test:{}:tag{}:{}:{}".format(self.checker_name, tag, t, i)
            for i in range(10)
        ]
        kvs = []
        for k in keys:
            kvs.extend([k, k])
        try:
            res, address = await self.client.mset(*kvs)
        except Exception as e:
            logger.error("REDIS_TEST: failed to mset {}: {} {}", keys, e, type(e))
            raise
        if not res:
            logger.info("REDIS_TEST: invalid response: {} address: {}", res, address)
            return

        self.kvs.update(keys)
        self.deleted_kvs.difference_update(keys)

    async def check_get(self):
        for k in self.kvs:
            try:
                v, address = await self.client.get(k)
            except Exception as e:
                logger.error("REDIS_TEST: failed to get {}: {} {}", k, e, type(e))
                raise
            if k != v:
                logger.error(
                    "INCONSISTENT: key: {}, expected {}, got {}, address {}",
                    k,
                    k,
                    v,
                    address,
                )
                raise CheckerError("INCONSISTENT DATA")

        for k in self.deleted_kvs:
            try:
                v, address = await self.client.get(k)
            except Exception as e:
                logger.error("REDIS_TEST: failed to get {}: {} {}", k, e, type(e))
                raise
            if v is not None:
                logger.error(
                    "INCONSISTENT: key: {}, expected {}, got {}, address {}",
                    k,
                    None,
                    v,
                    address,
                )
                raise CheckerError("INCONSISTENT DATA")

    async def check_mget(self):
        for keys in AioRedisClusterClient.group_by_slot(self.kvs):
            try:
                values, address = await self.client.mget(*keys)
            except Exception as e:
                logger.error("REDIS_TEST: failed to mget {}: {} {}", keys, e, type(e))
                raise

            for k, v in zip(keys, values):
                if k != v:
                    logger.error(
                        "INCONSISTENT: key: {}, expected {}, got {}, address {}",
                        k,
                        k,
                        v,
                        address,
                    )
                    raise CheckerError("INCONSISTENT DATA")

        for keys in AioRedisClusterClient.group_by_slot(self.deleted_kvs):
            try:
                values, address = await self.client.mget(*keys)
            except Exception as e:
                logger.error("REDIS_TEST: failed to get {}: {} {}", keys, e, type(e))
                raise
            for k, v in zip(keys, values):
                if v is not None:
                    logger.error(
                        "INCONSISTENT: key: {}, expected {}, got {}, address {}",
                        k,
                        None,
                        v,
                        address,
                    )
                    raise CheckerError("INCONSISTENT DATA")

    async def check_del(self):
        keys = list(self.kvs.pop() for _ in range(min(10, len(self.kvs))))
        half = len(keys) // 2
        await asyncio.gather(
            self.del_keys(keys[:half]), self.del_multi_keys(keys[half:]),
        )

    async def del_keys(self, keys):
        for k in list(keys):
            await self.del_multi_keys([k])

    async def del_multi_keys(self, keys):
        for ks in AioRedisClusterClient.group_by_slot(keys):
            try:
                values, address = await self.client.delete(*ks)
            except Exception as e:
                logger.error("REDIS_TEST: failed to del {}: {} {}", ks, e, type(e))
                raise
            self.kvs.difference_update(ks)
            self.deleted_kvs.update(ks)

    async def check_lua_mset(self):
        self.lua_value = str(time.time()).encode('utf-8')
        values = [self.lua_value for i in range(len(self.lua_keys))]
        await self.client.lua_mset(self.lua_keys, values)

    async def check_lua_mget(self):
        values, address = await self.client.lua_mget(self.lua_keys)
        inconsistent = False
        for k, v in zip(self.lua_keys, values):
            if v != self.lua_value:
                inconsistent = True
                logger.error("REDIS_TEST: incorrect value for key {}: {} != {}",
                    k, v, self.lua_value)
        if inconsistent:
            raise CheckerError("INCONSISTENT DATA")


class RandomChecker:
    def __init__(self, startup_nodes, concurrent_num):
        self.startup_nodes = startup_nodes
        self.concurrent_num = concurrent_num
        self.stopped = False
        self.init_signal_handler()

    def init_signal_handler(self):
        signal.signal(signal.SIGINT, self.handle_signal)

    def handle_signal(self, sig, frame):
        asyncio.get_event_loop().stop()
        self.stop()

    def stop(self):
        self.stopped = True

    async def run_one_checker(self, checker_name):
        client = AioRedisClusterClient(self.startup_nodes, timeout=3)
        await client.init_pool()
        while True:
            checker = KeyValueChecker(checker_name, client)
            await checker.loop_check()
            logger.info("checker {} restart".format(checker_name))

    async def run(self):
        checkers = [self.run_one_checker(str(i)) for i in range(self.concurrent_num)]
        await asyncio.gather(*checkers)


async def main(startup_nodes):
    try:
        await RandomChecker(startup_nodes, 10).run()
    except Exception as e:
        logger.error("checker stopped {} {}", e, type(e))

    logger.info("checker stopped but it will keep running so that we can see the logs")
    while True:
        await asyncio.sleep(3600)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise CheckerError("Missing service address")
    address = sys.argv[1]
    print("startup address:", address)
    asyncio.run(main([address]))
