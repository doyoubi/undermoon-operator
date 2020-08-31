import random
import itertools

import aioredis
import crc16
from loguru import logger


class RedisClientError(Exception):
    pass


class AioRedisClusterClient:
    MAX_SLOT = 16384

    @classmethod
    def group_by_slot(cls, keys):
        keys = [k for k in keys]
        it = itertools.groupby(keys, key_slot)
        for _, ks in it:
            yield list(ks)

    def __init__(self, startup_nodes, timeout, debug=False):
        self.startup_nodes = startup_nodes
        self.client_map = {}
        self.timeout = timeout
        self.debug = debug

    async def init_pool(self):
        for address in self.startup_nodes:
            await self.get_or_create_client(address)

    async def get_or_create_client(self, address):
        if address not in self.client_map:
            client = await aioredis.create_redis_pool(
                "redis://{}".format(address), timeout=self.timeout,
            )
            # Other coroutine may have already created
            # when we're creating redis pool.
            # Check again
            if address not in self.client_map:
                self.client_map[address] = client
            else:
                client.close()
                await client.wait_closed()
        return self.client_map[address]

    async def get(self, key):
        return await self.exec(lambda client: client.get(key, encoding="utf-8"))

    async def set(self, key, value):
        return await self.exec(lambda client: client.set(key, value))

    async def delete(self, key, *keys):
        return await self.exec(lambda client: client.delete(key, *keys))

    async def mget(self, key, *keys):
        return await self.exec(lambda client: client.mget(key, *keys, encoding="utf-8"))

    async def mset(self, *kvs):
        return await self.exec(lambda client: client.mset(*kvs))

    async def exec(self, send_func):
        address = random.choice(self.startup_nodes)
        client = await self.get_or_create_client(address)

        # Cover this case:
        # (1) random node
        # (2) importing node (PreSwitched not done)
        # (3) migrating node (PreSwitched done this time)
        # (4) importing node again!
        # Also, when scaling down the deleted node
        # will redirect to the cluster
        # so it needs another additional redirection.
        RETRY_TIMES = 5
        tried_addressess = [address]
        for i in range(0, RETRY_TIMES):
            try:
                return await send_func(client), address
            except Exception as e:
                if "MOVED" not in str(e):
                    raise RedisClientError("{}: {} {}".format(address, type(e), e))
                if i == RETRY_TIMES - 1:
                    logger.error("exceed max redirection times: {}", tried_addressess)
                    raise RedisClientError("{}: {}".format(address, e))
                former_address = address
                address = self.parse_moved(str(e))
                if self.debug:
                    logger.debug("moved {} -> {}", former_address, address)
                client = await self.get_or_create_client(address)
                tried_addressess.append(address)

    def parse_moved(self, response):
        segs = response.split(" ")
        if len(segs) != 3:
            raise RedisClientError("invalid moved response {}".format(response))
        address = segs[2]
        return address


def gen_tag_key(key):
    start = None
    for i, c in enumerate(key):
        if c == "{":
            start = i
            break
    end = None
    if start is not None:
        for i in range(start, len(key)):
            if key[i] == "}":
                end = i
                break
    if end is None:
        return key
    return key[start + 1 : end]


def key_slot(key):
    key = gen_tag_key(key)
    return crc16.crc16xmodem(key.encode("utf-8")) % AioRedisClusterClient.MAX_SLOT


def test_gen_tag_key():
    assert gen_tag_key("abc") == "abc"
    assert gen_tag_key("ab{c") == "ab{c"
    assert gen_tag_key("ab{}c") == ""
    assert gen_tag_key("ab{233}c") == "233"


test_gen_tag_key()
