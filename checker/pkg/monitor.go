package pkg

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"
)

type monitorManager struct {
	clients []*monitorClient
}

func newMonitorManager(addresses []string, bufCap int) *monitorManager {
	clients := make([]*monitorClient, 0, len(addresses))
	for _, addr := range addresses {
		clients = append(clients, newMonitorClient(addr, bufCap))
	}
	return &monitorManager{
		clients: clients,
	}
}

func (m *monitorManager) run(ctx context.Context) error {
	group, ctx := errgroup.WithContext(ctx)
	for _, c := range m.clients {
		client := c
		group.Go(func() error {
			return client.run(ctx)
		})
	}
	return group.Wait()
}

func (m *monitorManager) dump() error {
	for _, c := range m.clients {
		data := c.getData()
		fname := fmt.Sprintf("monitor-%s", c.addr)
		err := ioutil.WriteFile(fname, []byte(strings.Join(data, "")), 0644)
		if err != nil {
			log.Err(err).Str("address", c.addr).Msg("failed to dump monitor data")
			return err
		}
	}
	log.Info().Msg("successfully dump MONITOR data")
	return nil
}

type monitorClient struct {
	addr string
	buf  *ringBuf
	lock sync.Mutex
}

func newMonitorClient(addr string, bufCap int) *monitorClient {
	return &monitorClient{
		addr: addr,
		buf:  newRingBuf(bufCap),
		lock: sync.Mutex{},
	}
}

func (c *monitorClient) run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return errExit
		default:
		}

		if err := c.runMonitor(ctx); err != nil {
			log.Err(err).Msg("MONITOR error")
		}
		time.Sleep(time.Second)
	}
}

func (c *monitorClient) runMonitor(ctx context.Context) error {
	timeout := time.Second * 5
	conn, err := net.DialTimeout("tcp", c.addr, timeout)
	if err != nil {
		log.Err(err).Str("address", c.addr).Msg("failed to connect to redis")
		return err
	}
	defer conn.Close()

	monitorCmd := "*1\r\n$7\r\nMONITOR\r\n"
	w := bufio.NewWriter(conn)
	_, err = w.WriteString(monitorCmd)
	if err != nil {
		log.Err(err).Str("address", c.addr).Msg("failed to send MONITOR command to redis")
		return err
	}
	err = w.Flush()
	if err != nil {
		log.Err(err).Str("address", c.addr).Msg("failed to flush MONITOR command to redis")
		return err
	}

	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
	if err != nil {
		log.Err(err).Str("address", c.addr).Msg("failed to read MONITOR response")
		return err
	}

	okResLen := len("+OK\r\n")
	if len(line) != okResLen {
		err := fmt.Errorf("invalid MONITOR response: %s", line)
		log.Err(err).Msg("")
		return err
	}

	for {
		select {
		case <-ctx.Done():
			break
		default:
		}

		line, err = r.ReadString('\n')
		if err != nil {
			log.Err(err).Str("address", c.addr).Msg("failed to read MONITOR line")
			return err
		}

		c.lock.Lock()
		c.buf.add(line)
		c.lock.Unlock()
	}
}

func (c *monitorClient) getData() []string {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.buf.data()
}

// Not thread-safe
//
// 1. empty: start == end
// 2. not empty and start < end
// 3. not empty and start > end
// 4. full: start == end + 1
type ringBuf struct {
	buf   []string
	cap   int
	start int
	end   int
}

func newRingBuf(cap int) *ringBuf {
	return &ringBuf{
		buf:   make([]string, cap, cap),
		cap:   cap,
		start: 0,
		end:   0,
	}
}

func (r *ringBuf) add(item string) {
	if r.len()+1 == r.cap {
		// Full. Remove one.
		r.start = (r.start + 1) % r.cap
	}
	r.buf[r.end] = item
	r.end = (r.end + 1) % r.cap
}

func (r *ringBuf) len() int {
	if r.start <= r.end {
		return r.end - r.start
	}
	return r.end + r.cap - r.start
}

func (r *ringBuf) data() []string {
	l := r.len()
	d := make([]string, l, l)
	if r.start <= r.end {
		copy(d, r.buf[r.start:r.end])
		return d
	}

	copy(d, r.buf[r.start:r.cap])
	if r.end != 0 {
		copy(d[r.cap-r.start:], r.buf[0:r.end])
	}
	return d
}
