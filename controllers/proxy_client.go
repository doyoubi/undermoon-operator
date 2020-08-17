package controllers

import (
	"context"

	"github.com/go-redis/redis/v8"
)

type serverProxyClient struct {
	redisClient *redis.Client
}

func (client *serverProxyClient) getEpoch() (int64, error) {
	cmd := redis.NewIntCmd(context.TODO(), "UMCTL", "GETEPOCH")
	err := client.redisClient.Process(context.TODO(), cmd)
	if err != nil {
		return 0, err
	}
	epoch, err := cmd.Result()
	return epoch, err
}

type serverProxyClientPool struct {
	redisPool *redisClientPool
}

func newServerProxyClientPool() *serverProxyClientPool {
	return &serverProxyClientPool{
		redisPool: newRedisClientPool(),
	}
}

func (pool *serverProxyClientPool) getClient(serverProxyAddress string) *serverProxyClient {
	client := pool.redisPool.getClient(serverProxyAddress)
	return &serverProxyClient{
		redisClient: client,
	}
}

func (pool *serverProxyClientPool) getEpoch(serverProxyAddress string) (int64, error) {
	c := pool.getClient(serverProxyAddress)
	return c.getEpoch()
}
