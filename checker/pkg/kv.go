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
	"gonum.org/v1/gonum/stat/distuv"
)

const maxKeysNum = slotNumber * 8

var errExit = errors.New("exit error")

type kvChecker struct {
	id     int
	client *CheckerClusterClient
	rl     *rate.Limiter
	d      distuv.Normal

	keys     [slotNumber]map[string]string
	luaKeys  []string
	luaValue string
}

func newKVChecker(id int, client *CheckerClusterClient, ops int64, st *slotTags) *kvChecker {
	keys := [slotNumber]map[string]string{}
	for s := 0; s != slotNumber; s++ {
		keys[s] = make(map[string]string)
	}
	t := time.Now()
	for i := 0; i != maxKeysNum; i++ {
		if i >= maxKeysNum {
			break
		}
		s := i % slotNumber
		k := genKey(t, id, s, i, st.getTag(s))
		keys[s][k] = ""
	}

	const luaKeysNum = 10
	luaKeys := make([]string, 0, luaKeysNum)
	for i := 0; i != luaKeysNum; i++ {
		k := genLuaKey(t, id, i)
		luaKeys = append(luaKeys, k)
	}

	const burst = 100
	rl := rate.NewLimiter(rate.Limit(ops), burst)

	return &kvChecker{
		id:       id,
		client:   client,
		rl:       rl,
		d:        distuv.UnitNormal,
		keys:     keys,
		luaKeys:  luaKeys,
		luaValue: "",
	}
}

func genKey(t time.Time, ckID, slot, keyID int, tag string) string {
	ts := strconv.FormatInt(t.UnixNano(), 10)
	return fmt.Sprintf("test:%s:%d:%d:{%s}:%d", ts, ckID, slot, tag, keyID)
}

func genLuaKey(t time.Time, ckID, keyID int) string {
	ts := strconv.FormatInt(t.UnixNano(), 10)
	return fmt.Sprintf("test:%s:lua:%d:{tag}:%d:%d", ts, ckID, keyID, time.Now().Second())
}

func genNewValue(key string) string {
	t := strconv.FormatInt(time.Now().UnixNano(), 10)
	return fmt.Sprintf("%s:%s", key, t)
}

func (ck *kvChecker) loopCheck(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return errExit
		default:
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
		return ck.checkMSet(ctx)
	case 2:
		return ck.checkLuaMSet(ctx)
	case 3:
		return ck.checkGet(ctx)
	case 4:
		return ck.checkMGet(ctx)
	case 5:
		return ck.checkLuaMGet(ctx)
	case 6:
		return ck.checkDel(ctx)
	default:
	}

	return nil
}

func (ck *kvChecker) randomChooseKeysInSameSlot(n int) (int, map[string]string) {
	if n == 0 {
		return 0, nil
	}

	slot := chooseSlotFromNormDist(ck.d)
	keys := make(map[string]string)
	for k, v := range ck.keys[slot] {
		keys[k] = v
		if len(keys) == n {
			break
		}
	}
	return slot, keys
}

func (ck *kvChecker) randomChooseKeys(n int) map[string]string {
	if n == 0 {
		return nil
	}

	keys := make(map[string]string)
	for i := 0; i != n; i++ {
		slot := chooseSlotFromNormDist(ck.d)
		k, v := randomGetKey(ck.keys[slot])
		keys[k] = v
	}
	return keys
}

func (ck *kvChecker) checkSet(ctx context.Context) error {
	const keysNum = 10
	if err := ck.rl.WaitN(ctx, keysNum); err != nil {
		return err
	}

	kvs := ck.randomChooseKeys(keysNum)

	for k := range kvs {
		v := genNewValue(k)
		address, err := ck.client.Set(k, v)
		if err != nil {
			log.Err(err).Str("node", address).Msg("failed to SET")
			return err
		}
		slot := Slot(k)
		ck.keys[slot][k] = v
	}

	return nil
}

func (ck *kvChecker) checkMSet(ctx context.Context) error {
	const keysNum = 10
	if err := ck.rl.WaitN(ctx, keysNum); err != nil {
		return err
	}

	slot, kvs := ck.randomChooseKeysInSameSlot(keysNum)

	args := make([]string, 0, 2*keysNum)
	for k := range kvs {
		v := genNewValue(k)
		args = append(args, k, v)
		kvs[k] = v
	}

	address, err := ck.client.MSet(args)
	if err != nil {
		log.Err(err).Str("node", address).Msg("failed to MSET")
		return err
	}

	for k, v := range kvs {
		ck.keys[slot][k] = v
	}
	return nil
}

func (ck *kvChecker) checkGet(ctx context.Context) error {
	const keysNum = 10
	if err := ck.rl.WaitN(ctx, keysNum); err != nil {
		return err
	}

	kvs := ck.randomChooseKeys(keysNum)
	for key, value := range kvs {
		v, address, err := ck.client.Get(key)
		if err != nil {
			log.Err(err).Str("node", address).Msg("failed to GET")
			return err
		}
		s := nil2Empty(v)
		if s != value {
			err := fmt.Errorf("INCONSISTENT: key: %s, got '%s', expected '%s', address %s",
				key, s, value, address)
			log.Err(err).Send()
			return err
		}
	}

	return nil
}

func (ck *kvChecker) checkMGet(ctx context.Context) error {
	const keysNum = 10
	_, kvs := ck.randomChooseKeysInSameSlot(keysNum)
	if len(kvs) == 0 {
		return nil
	}

	if err := ck.rl.WaitN(ctx, keysNum); err != nil {
		return err
	}

	keys := getKeys(kvs)

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
		s := nil2Empty(values[i])
		expected := kvs[keys[i]]
		if s == expected {
			continue
		}
		err = fmt.Errorf("INCONSISTENT: key: %s, got '%s', expected '%s' address %s",
			keys[i], s, expected, address)
		log.Err(err).Send()
	}
	return err
}

func (ck *kvChecker) checkDel(ctx context.Context) error {
	const maxDelKeys = 10
	slot, kvs := ck.randomChooseKeysInSameSlot(maxDelKeys)
	if len(kvs) == 0 {
		return nil
	}

	if err := ck.rl.WaitN(ctx, maxDelKeys); err != nil {
		return err
	}

	keys := getKeys(kvs)

	address, err := ck.client.Del(keys)
	if err != nil {
		log.Err(err).Str("node", address).Strs("keys", keys).Msg("failed to DEL")
		return err
	}

	for _, k := range keys {
		ck.keys[slot][k] = ""
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
		log.Err(err).Str("node", address).Strs("keys", keys).Msg("failed to Lua MSet")
		return err
	}

	ck.luaValue = value
	return nil
}

func (ck *kvChecker) checkLuaMGet(ctx context.Context) error {
	keys := ck.luaKeys

	if err := ck.rl.WaitN(ctx, len(keys)); err != nil {
		return err
	}

	values, address, err := ck.client.LuaMGet(keys)
	if err != nil {
		log.Err(err).Str("node", address).Strs("keys", keys).Msg("failed to Lua MGet")
		return err
	}

	for i := 0; i != len(keys); i++ {
		v := nil2Empty(values[i])
		if v == ck.luaValue {
			continue
		}
		err = fmt.Errorf("INCONSISTENT: incorrect value for key %s: got '%s' expected '%s'",
			keys[i], v, ck.luaValue)
		log.Err(err).Str("node", address).Send()
	}
	return err
}

// RunKvCheckerService runs the kvChecker
func RunKvCheckerService(ctx context.Context, startupNode string, ops int64) {
	const checkerNum = 3

	st := newSlotTags()

	group, ctx := errgroup.WithContext(ctx)
	for i := 0; i != checkerNum; i++ {
		id := i
		group.Go(func() error {
			c := NewCheckerClusterClient(startupNode, time.Second, false)
			for {
				ck := newKVChecker(id, c, ops, st)
				if err := ck.loopCheck(ctx); err != nil {
					return err
				}
				log.Info().Int("id", id).Msg("refresh checker")
			}
		})
	}

	_ = group.Wait()

	log.Info().Msg("checker stopped but it will keep running so that we can see the logs")
	for {
		time.Sleep(time.Hour)
	}
}
