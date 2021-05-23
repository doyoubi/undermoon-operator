package pkg

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
)

// CheckerClusterClient implements redis cluster clients
type CheckerClusterClient struct {
	startupNode string
	timeout     time.Duration
	pool        sync.Map
	ctx         context.Context
	debug       bool
}

// NewCheckerClusterClient creates CheckerClusterClient
func NewCheckerClusterClient(startupNode string, timeout time.Duration, debug bool) *CheckerClusterClient {
	return &CheckerClusterClient{
		startupNode: startupNode,
		timeout:     timeout,
		pool:        sync.Map{},
		ctx:         context.Background(),
		debug:       debug,
	}
}

// Get implements GET command
func (client *CheckerClusterClient) Get(key string) (*string, error) {
	v, err := client.Exec(func(c *redis.Client) (interface{}, error) {
		return c.Get(client.ctx, key).Result()
	})
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s := v.(string)
	return &s, err
}

// Set implements SET command
func (client *CheckerClusterClient) Set(key, value string) error {
	_, err := client.Exec(func(c *redis.Client) (interface{}, error) {
		return c.Set(client.ctx, key, value, 0).Result()
	})
	return err
}

// Del implements DEL command
func (client *CheckerClusterClient) Del(key, value string) error {
	_, err := client.Exec(func(c *redis.Client) (interface{}, error) {
		return c.Del(client.ctx, key).Result()
	})
	return err
}

// MGet implements MGET command
func (client *CheckerClusterClient) MGet(keys []string) ([]*string, error) {
	vs, err := client.Exec(func(c *redis.Client) (interface{}, error) {
		return c.MGet(client.ctx, keys...).Result()
	})

	if err != nil {
		return nil, err
	}

	vals := vs.([]interface{})
	values := make([]*string, 0, len(vals))
	for _, v := range vals {
		if v == nil {
			values = append(values, nil)
			continue
		}
		s := v.(string)
		values = append(values, &s)
	}
	return values, err
}

// MSet implements MSET command
func (client *CheckerClusterClient) MSet(values []string) error {
	_, err := client.Exec(func(c *redis.Client) (interface{}, error) {
		vs := make([]interface{}, 0, len(values))
		for _, v := range values {
			vs = append(vs, v)
		}
		return c.MSet(client.ctx, vs...).Result()
	})
	return err
}

// LuaMGet implements atomic MGET command using EVAL
func (client *CheckerClusterClient) LuaMGet(keys []string) ([]*string, error) {
	vs, err := client.Exec(func(c *redis.Client) (interface{}, error) {
		return c.Eval(client.ctx, luaMSetScript, keys).Result()
	})

	vals := vs.([]interface{})
	values := make([]*string, 0, len(vals))
	for _, v := range vals {
		if v == nil {
			values = append(values, nil)
			continue
		}
		s := v.(string)
		values = append(values, &s)
	}
	return values, err
}

// LuaMSet implements atomic MSET command using EVAL
func (client *CheckerClusterClient) luaMSet(keys []string, values []string) error {
	_, err := client.Exec(func(c *redis.Client) (interface{}, error) {
		vs := make([]interface{}, 0, len(values))
		for _, v := range values {
			vs = append(vs, v)
		}
		return c.Eval(client.ctx, luaMSetScript, keys, vs...).Result()
	})
	return err
}

// Exec can be used to send command with redirection supported
func (client *CheckerClusterClient) Exec(sendFunc func(*redis.Client) (interface{}, error)) (interface{}, error) {
	address := client.startupNode
	c := client.getOrCreateClient(address)

	const maxRetryTimes = 5
	triedAddressess := []string{address}
	innerInfos := []interface{}{}

	for i := 0; i != maxRetryTimes; i++ {
		r, err := sendFunc(c)
		if err == nil {
			return r, nil
		}

		address, err = client.parseMoved(err)
		if err != nil {
			return nil, err
		}

		if client.debug && i != 0 {
			umctlInfo, err := client.sendUmctlInfo(client.ctx, c)
			if err != nil {
				return nil, err
			}
			innerInfos = append(innerInfos, umctlInfo)
		}

		if i+1 == maxRetryTimes {
			break
		}

		triedAddressess = append(triedAddressess, address)
		c = client.getOrCreateClient(address)
	}

	err := fmt.Errorf("exceed max redirection times: %+v %+v", triedAddressess, innerInfos)
	log.Err(err).Send()
	return nil, err
}

func (client *CheckerClusterClient) getOrCreateClient(address string) *redis.Client {
	oldC, ok := client.pool.Load(address)
	if ok {
		return oldC.(*redis.Client)
	}

	c := redis.NewClient(&redis.Options{
		Addr:         address,
		DialTimeout:  client.timeout,
		ReadTimeout:  client.timeout,
		WriteTimeout: client.timeout,
	})

	oldC, _ = client.pool.LoadOrStore(address, c)
	return oldC.(*redis.Client)
}

// Return err directly if the response is not MOVED reply.
func (client *CheckerClusterClient) parseMoved(err error) (string, error) {
	e := err.Error()
	if !strings.Contains(e, "MOVED") {
		return "", err
	}

	segs := strings.Split(e, " ")
	if len(segs) != 3 {
		return "", fmt.Errorf("invalid MOVED response: %s", e)
	}

	address := segs[2]
	return address, nil
}

func (client *CheckerClusterClient) sendUmctlInfo(ctx context.Context, c *redis.Client) (interface{}, error) {
	cmd := redis.NewCmd(ctx, "UMCTL", "INFO")
	err := c.Process(ctx, cmd)
	if err != nil {
		log.Err(err).Msg("failed to send UMCTL INFO")
		return nil, err
	}
	return cmd.Result()
}

const (
	luaMSetScript = `for i, k in pairs(KEYS) do
  redis.call('SET', k, ARGV[i])
end
return "OK"`
	luaMGetScript = `local values = {}
for i, k in pairs(KEYS) do
  values[i] = redis.call('GET', k)
end
return values`
)
