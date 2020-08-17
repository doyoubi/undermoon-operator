package controllers

import (
	"context"

	"github.com/go-redis/redis/v8"
)

type coordinatorClient struct {
	redisClient *redis.Client
}

func (client *coordinatorClient) setBrokerAddress(brokerAddress string) error {
	cmd := redis.NewStringCmd(context.TODO(), "CONFIG", "SET", "brokers", brokerAddress)
	err := client.redisClient.Process(context.TODO(), cmd)
	if err != nil {
		return err
	}
	_, err = cmd.Result()
	return err
}

type coordinatorClientPool struct {
	redisPool *redisClientPool
}

func newCoordinatorClientPool() *coordinatorClientPool {
	return &coordinatorClientPool{
		redisPool: newRedisClientPool(),
	}
}

func (pool *coordinatorClientPool) getClient(coordAddress string) *coordinatorClient {
	client := pool.redisPool.getClient(coordAddress)
	return &coordinatorClient{
		redisClient: client,
	}
}

func (pool *coordinatorClientPool) setBrokerAddress(coordAddress, brokerAddress string) error {
	c := pool.getClient(coordAddress)
	return c.setBrokerAddress(brokerAddress)
}
