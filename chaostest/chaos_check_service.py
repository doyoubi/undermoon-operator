import sys
import time
import random
import signal
import asyncio

from loguru import logger

from cluster_client import AioRedisClusterClient


class CheckerError(Exception):
    pass


class CheckResult:
    def __init__(
        self, success_count, failed_count, consistent_count, not_consistent_count
    ):
        self.success_count = success_count
        self.failed_count = failed_count
        self.consistent_count = consistent_count
        self.not_consistent_count = not_consistent_count

    @classmethod
    def merge(cls, results):
        res = CheckResult(0, 0, 0, 0)
        for r in results:
            res.success_count += r.success_count
            res.failed_count += r.failed_count
            res.consistent_count += r.consistent_count
            res.not_consistent_count += r.not_consistent_count
        return res

    def error_exists(self) -> bool:
        return self.failed_count > 0 or self.not_consistent_count > 0

    def success_rate(self):
        s = self.success_count + self.failed_count
        if not s:
            return None
        return float(self.success_count) / s

    def consistent_rate(self):
        s = self.consistent_count + self.not_consistent_count
        if not s:
            return None
        return float(self.consistent_count) / s


def format_rate(r) -> str:
    if r is None:
        return "nil"
    return "{:.2f}%".format(r * 100)


SUCCESS_RESULT = CheckResult(1, 0, 0, 0)
CONSISTENT_RESULT = CheckResult(1, 0, 1, 0)
FAILURE_RESULT = CheckResult(0, 1, 0, 0)
INCONSISTENT_RESULT = CheckResult(1, 0, 0, 1)


# This checker won't stop on error
# and just keep tracking the success rate and consistent rate.
class ChaosKeyValueChecker:
    MAX_KVS = 10000

    def __init__(self, checker_name, client):
        self.checker_name = checker_name
        self.client = client
        self.kvs = set()
        self.deleted_kvs = set()

    async def loop_check(self):
        while True:
            if len(self.kvs) >= self.MAX_KVS or len(self.deleted_kvs) >= self.MAX_KVS:
                await self.check_del_keys(self.kvs)
                return
            await self.checker_key_value()
            await asyncio.sleep(1)

    async def checker_key_value(self):
        try:
            n = random.randint(0, 10)
            if n < 4:
                results = await asyncio.gather(self.check_set(), self.check_mset(),)
                set_result = results[0]
                mset_result = results[1]
                if set_result.error_exists():
                    logger.info(
                        "SET: success {}", format_rate(set_result.success_rate()),
                    )
                if mset_result.error_exists():
                    logger.info(
                        "MSET: success {}", format_rate(mset_result.success_rate()),
                    )
            elif n < 8:
                results = await asyncio.gather(self.check_get(), self.check_mget(),)
                get_result = results[0]
                mget_result = results[1]
                if get_result.error_exists():
                    logger.info(
                        "GET: success {} consistent {}",
                        format_rate(get_result.success_rate()),
                        format_rate(get_result.consistent_rate()),
                    )
                if mget_result.error_exists():
                    logger.info(
                        "MGET: success {} consistent {}",
                        format_rate(mget_result.success_rate()),
                        format_rate(mget_result.consistent_rate()),
                    )
            else:
                keys = list(self.kvs.pop() for _ in range(min(10, len(self.kvs))))
                half = len(keys) // 2
                results = await asyncio.gather(
                    self.check_del_keys(keys[:half]),
                    self.check_del_multi_keys(keys[half:]),
                )
                del_result = results[0]
                del_multi_result = results[1]
                if del_result.error_exists():
                    logger.info(
                        "DEL: success {}", format_rate(del_result.success_rate()),
                    )
                if del_multi_result.error_exists():
                    logger.info(
                        "DEL multi: success {}",
                        format_rate(del_multi_result.success_rate()),
                    )
        except Exception as e:
            logger.exception("REDIS_TEST_FAILED: {} {}", e, type(e))
            raise

    async def check_set(self) -> CheckResult:
        if len(self.kvs) >= self.MAX_KVS:
            return

        t = get_current_time()
        keys = ["test:{}:{}:{}".format(self.checker_name, t, i) for i in range(10)]
        futs = [self.check_set_single_key(k) for k in keys]
        results = await asyncio.gather(*futs)
        return CheckResult.merge(results)

    async def check_set_single_key(self, key) -> CheckResult:
        try:
            res, address = await self.client.set(key, key)
        except Exception as e:
            logger.error("REDIS_TEST: failed to set {}: {} {}", key, e, type(e))
            return FAILURE_RESULT
        if not res:
            logger.error("REDIS_TEST: invalid response: {} address: {}", res, address)
            return FAILURE_RESULT
        self.kvs.add(key)
        self.deleted_kvs.discard(key)
        return SUCCESS_RESULT

    async def check_mset(self) -> CheckResult:
        if len(self.kvs) >= self.MAX_KVS:
            return

        t = get_current_time()
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
            return FAILURE_RESULT
        if not res:
            logger.info("REDIS_TEST: invalid response: {} address: {}", res, address)
            return FAILURE_RESULT

        self.kvs.update(keys)
        self.deleted_kvs.difference_update(keys)
        return SUCCESS_RESULT

    async def check_get(self) -> CheckResult:
        res = CheckResult(0, 0, 0, 0)
        for ks in list(group_list(self.kvs, 10)):
            futs = [self.check_get_single_key(k) for k in ks]
            results = await asyncio.gather(*futs)
            results.append(res)
            res = CheckResult.merge(results)
        return res

    async def check_get_single_key(self, key):
        try:
            value, address = await self.client.get(key)
        except Exception as e:
            logger.error("REDIS_TEST: failed to get {}: {} {}", key, e, type(e))
            return FAILURE_RESULT
        if key != value:
            logger.error(
                "INCONSISTENT: key: {}, expected {}, got {}, address {}",
                key,
                key,
                value,
                address,
            )
            self.kvs.discard(key)
            return INCONSISTENT_RESULT
        return CONSISTENT_RESULT

    async def check_mget(self) -> CheckResult:
        res = CheckResult(0, 0, 0, 0)
        for keys in AioRedisClusterClient.group_by_slot(self.kvs):
            try:
                values, address = await self.client.mget(*keys)
            except Exception as e:
                logger.error("REDIS_TEST: failed to mget {}: {} {}", keys, e, type(e))
                res.failed_count += 1
                continue

            res.success_count += 1
            for k, v in zip(keys, values):
                if k != v:
                    logger.error(
                        "INCONSISTENT: key: {}, expected {}, got {}, address {}",
                        k,
                        k,
                        v,
                        address,
                    )
                    res.not_consistent_count += 1
                    self.kvs.discard(k)
                else:
                    res.consistent_count += 1

        for keys in AioRedisClusterClient.group_by_slot(self.deleted_kvs):
            try:
                values, address = await self.client.mget(*keys)
            except Exception as e:
                logger.error("REDIS_TEST: failed to get {}: {} {}", keys, e, type(e))
                res.failed_count += 1
                continue
            res.success_count += 1
            for k, v in zip(keys, values):
                if v is not None:
                    logger.error(
                        "INCONSISTENT: key: {}, expected {}, got {}, address {}",
                        k,
                        None,
                        v,
                        address,
                    )
                    res.not_consistent_count += 1
                    self.deleted_kvs.discard(k)
                    self.kvs.add(k)
                else:
                    res.consistent_count += 1

        return res

    async def check_del_keys(self, keys):
        res = CheckResult(0, 0, 0, 0)
        for ks in list(group_list(keys, 10)):
            futs = [self.check_del_multi_keys([k]) for k in ks]
            results = await asyncio.gather(*futs)
            results.append(res)
            res = CheckResult.merge(results)
        return res

    async def check_del_multi_keys(self, keys) -> CheckResult:
        res = CheckResult(0, 0, 0, 0)
        for ks in AioRedisClusterClient.group_by_slot(keys):
            try:
                values, address = await self.client.delete(*ks)
            except Exception as e:
                logger.error("REDIS_TEST: failed to del {}: {} {}", ks, e, type(e))
                res.failed_count += 1
                continue
            res.success_count += 1
            self.kvs.difference_update(ks)
            self.deleted_kvs.update(ks)
        return res


class ChaosRandomChecker:
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
            checker = ChaosKeyValueChecker(checker_name, client)
            await checker.loop_check()
            logger.info("checker {} restart".format(checker_name))

    async def run(self):
        checkers = [self.run_one_checker(str(i)) for i in range(self.concurrent_num)]
        await asyncio.gather(*checkers)


def get_current_time():
    return int(time.time() * 1000000)


def group_list(l, group_size):
    assert group_size > 0
    group = []
    for e in l:
        group.append(e)
        if len(group) == group_size:
            yield group
            group = []
    yield group


async def main(startup_nodes):
    try:
        await ChaosRandomChecker(startup_nodes, 3).run()
    except Exception as e:
        logger.exception("checker stopped {} {}", e, type(e))


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise CheckerError("Missing service address")
    address = sys.argv[1]
    print("startup address:", address)
    asyncio.run(main([address]))
