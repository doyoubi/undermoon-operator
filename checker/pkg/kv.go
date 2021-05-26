package pkg

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

const maxKeysNum = 10000

var errExit = errors.New("exit error")

type kvChecker struct {
	id     int
	client *CheckerClusterClient
	rl *rate.Limiter

	keys        map[string]struct{}
	groupedKeys map[int]map[string]struct{}
	deletedKeys map[string]struct{}
	luaKeys     []string
	luaValue    *string
}

func newKVChecker(id int, client *CheckerClusterClient, ops int64) *kvChecker {
	const luaKeysNum = 10
	luaKeys := make([]string, 0, luaKeysNum)
	for i := 0; i != luaKeysNum; i++ {
		k := fmt.Sprintf("test:lua:%d:{tag}:%d:%d", id, time.Now().Second(), i)
		luaKeys = append(luaKeys, k)
	}

	r := time.Second / time.Duration(ops)
	const burst = 100
	rl := rate.NewLimiter(rate.Every(r), burst)

	return &kvChecker{
		id:          id,
		client:      client,
		rl: rl,
		keys:        make(map[string]struct{}),
		groupedKeys: make(map[int]map[string]struct{}),
		deletedKeys: make(map[string]struct{}),
		luaKeys:     luaKeys,
		luaValue:    nil,
	}
}

func (ck *kvChecker) loopCheck(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return errExit
		default:
		}

		if len(ck.keys) >= maxKeysNum || len(ck.deletedKeys) >= maxKeysNum {
			return nil
		}

		if err := ck.checkKeyValue(ctx); err != nil {
			return err
		}
	}
}

func (ck *kvChecker) checkKeyValue(ctx context.Context) error {
	n := rand.Int() % 7
	switch n {
	case 0:
		return ck.checkSet(ctx)
	case 1:
		return ck.checkMGet(ctx)
	case 2:
		return ck.checkLuaMSet(ctx)
	case 3:
		return ck.checkGet(ctx)
	case 4:
		return ck.checkMSet(ctx)
	case 5:
		return ck.checkLuaMGet(ctx)
	case 6:
		return ck.checkDel(ctx)
	default:
	}

	return nil
}

func (ck *kvChecker) checkSet(ctx context.Context) error {
	if len(ck.keys) >= maxKeysNum {
		return nil
	}

	const keysNum = 10
	if err := ck.rl.WaitN(ctx, keysNum); err != nil {
		return err
	}

	t := strconv.FormatInt(int64(time.Now().Nanosecond()), 10)
	for i := 0; i != keysNum; i++ {
		k := fmt.Sprintf("test:%d:%s:%s", ck.id, t, i)
		address, err := ck.client.Set(k, k)
		if err != nil {
			log.Err(err).Str("node", address).Msg("failed to SET")
			return err
		}
		ck.addKey(Slot(k), k)
	}

	return nil
}

func (ck *kvChecker) addKey(slot int, k string) {
	ck.keys[k] = struct{}{}
	delete(ck.deletedKeys, k)

	_, ok := ck.groupedKeys[slot]
	if !ok {
		ck.groupedKeys[slot] = make(map[string]struct{})
	}
	ck.groupedKeys[slot][k] = struct{}{}
}

func (ck *kvChecker) checkMSet(ctx context.Context) error {
	if len(ck.keys) >= maxKeysNum {
		return nil
	}

	const keysNum = 10
	if err := ck.rl.Wait(ctx); err != nil {
		return err
	}

	t := strconv.FormatInt(int64(time.Now().Nanosecond()), 10)
	keys := make([]string, 0, keysNum)
	slot := rand.Int() % slotNumber
	for i := 0; i != keysNum; i++ {
		k := fmt.Sprintf("test:%d:tag{%d}:%s:%d", ck.id, slot, t, i)
		keys = append(keys, k)
	}

	kvs := make([]string, 0, 2*keysNum)
	for _, k := range keys {
		kvs = append(kvs, k)
		kvs = append(kvs, k)
	}

	address, err := ck.client.MSet(kvs)
	if err != nil {
		log.Err(err).Str("node", address).Msg("failed to MSET")
		return err
	}

	for _, k := range keys {
		ck.addKey(slot, k)
	}
	return nil
}

func (ck *kvChecker) checkGet(ctx context.Context) error {
	if err := ck.checkGetExistingKey(ctx); err != nil {
		return err
	}
	return ck.checkGetDeletedKey(ctx)
}

func (ck *kvChecker) checkGetExistingKey(ctx context.Context) error {
	const keysNum = 10
	if err := ck.rl.WaitN(ctx, keysNum); err != nil {
		return err
	}

	l := len(ck.keys)
	k := (l + keysNum - 1) / keysNum
	i := 0
	for key := range ck.keys {
		if (i % k) != 0 {
			continue
		}
		v, address, err := ck.client.Get(key)
		if err != nil {
			log.Err(err).Str("node", address).Msg("failed to GET")
			return err
		}
		if v == nil || key != *v {
			err := fmt.Errorf("INCONSISTENT: key: %s, got %+v, address %s",
				key, v, address)
			log.Err(err).Send()
			return err
		}
	}

	return nil
}

func (ck *kvChecker) checkGetDeletedKey(ctx context.Context) error {
	const keysNum = 10
	if err := ck.rl.WaitN(ctx, keysNum); err != nil {
		return err
	}

	l := len(ck.deletedKeys)
	k := (l + keysNum - 1) / keysNum
	i := 0
	for key := range ck.deletedKeys {
		if (i % k) != 0 {
			continue
		}
		v, address, err := ck.client.Get(key)
		if err != nil {
			log.Err(err).Str("node", address).Msg("failed to GET")
			return err
		}
		if v != nil {
			err := fmt.Errorf("INCONSISTENT: deleted key: %s, got %s, address %s",
				key, *v, address)
			log.Err(err).Send()
			return err
		}
	}

	return nil
}

func (ck *kvChecker) checkMGet(ctx context.Context) error {
	_, keys := ck.getKeysInSameSlot()
	if len(keys) == 0 {
		return nil
	}

	if err := ck.rl.Wait(ctx); err != nil {
		return err
	}

	values, address, err := ck.client.MGet(keys)
	if err != nil {
		log.Err(err).Str("node", address).Msg("failed to MGET")
		return err
	}

	if len(keys) != len(values) {
		err := fmt.Errorf("invalid values len %d != %d", len(keys), len(values))
		log.Err(err).Str("node", address).Send()
		return err
	}

	for i := 0; i != len(keys); i++ {
		if values[i] != nil && keys[i] == *values[i] {
			continue
		}
		err := fmt.Errorf("INCONSISTENT: key: %s, got %+v, address %s", keys[i], values[i], address)
		log.Err(err).Send()
		return err
	}
	return nil
}

func (ck *kvChecker) getKeysInSameSlot() (int, []string) {
	if len(ck.groupedKeys) == 0 {
		return 0, nil
	}

	idx := rand.Int() % len(ck.groupedKeys)
	keys := []string{}
	i := 0
	for slot, m := range ck.groupedKeys {
		if i != idx {
			continue
		}
		for k := range m {
			keys = append(keys, k)
		}
		return slot, keys
	}
	return 0, nil
}

func (ck *kvChecker) checkDel(ctx context.Context) error {
	const maxDelKeys = 10
	slot, keys := ck.getKeysInSameSlot()
	if len(keys) == 0 {
		return nil
	}
	if len(keys) > maxDelKeys {
		keys = keys[:maxDelKeys]
	}

	if err := ck.rl.Wait(ctx); err != nil {
		return err
	}

	address, err := ck.client.Del(keys)
	if err != nil {
		log.Err(err).Str("node", address).Msg("failed to DEL")
		return err
	}

	for _, k := range keys {
		delete(ck.keys, k)
		delete(ck.groupedKeys[slot], k)
		ck.deletedKeys[k] = struct{}{}
	}

	return nil
}

func (ck *kvChecker) checkLuaMSet(ctx context.Context) error {
	keys := ck.luaKeys
	values := make([]string, 0, len(keys))

	if err := ck.rl.WaitN(ctx, len(keys)); err != nil {
		return err
	}

	t := strconv.FormatInt(int64(time.Now().Nanosecond()), 10)
	value := fmt.Sprintf("%s:%s", t, uuid.NewString())
	for i := 0; i != len(keys); i++ {
		values = append(values, value)
	}
	address, err := ck.client.luaMSet(keys, values)
	if err != nil {
		log.Err(err).Str("node", address).Msg("failed to Lua MSet")
		return err
	}

	ck.luaValue = &value
	return nil
}

func (ck *kvChecker) checkLuaMGet(ctx context.Context) error {
	keys := ck.luaKeys

	if err := ck.rl.WaitN(ctx, len(keys)); err != nil {
		return err
	}

	values, address, err := ck.client.LuaMGet(keys)
	if err != nil {
		log.Err(err).Str("node", address).Msg("failed to Lua MGet")
		return err
	}

	for i := 0; i != len(keys); i++ {
		// Both nil or equal
		if values[i] == ck.luaValue || *values[i] == *ck.luaValue {
			continue
		}
		currLuaValue := "nil"
		if ck.luaValue != nil {
			currLuaValue = *ck.luaValue
		}
		value := "nil"
		if values[i] != nil {
			value = *values[i]
		}
		err = fmt.Errorf("INCONSISTENT: incorrect value for key %s: '%s' != '%s'", keys[i], currLuaValue, value)
		log.Err(err).Str("node", address).Send()
	}
	return err
}

// RunKvCheckerService runs the kvChecker
func RunKvCheckerService(ctx context.Context, startupNode string, ops int64) {
	const checkerNum = 3

	group, ctx := errgroup.WithContext(ctx)
	for i := 0; i != checkerNum; i++ {
		id := i
		group.Go(func() error {
			c := NewCheckerClusterClient(startupNode, time.Second, false)
			for {
				ck := newKVChecker(id, c, ops)
				if err := ck.loopCheck(ctx); err != nil {
					return err
				}
				log.Info().Int("id", id).Msg("refresh checker")
			}
			return nil
		})
	}

	group.Wait()

	log.Info().Msg("checker stopped but it will keep running so that we can see the logs")
	for {
		time.Sleep(time.Hour)
	}
}
