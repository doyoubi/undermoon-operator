package pkg

import (
	"bufio"
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"net"
	"time"
)

type monitorManager struct {
	srcRedis monitorClient
	dstRedis monitorClient
}

type monitorClient struct {
	addr string
	data *ringBuf
}

func newMonitorClient(addr string, bufCap int) *monitorClient {
	return &monitorClient{
		addr: addr,
		data: newRingBuf(bufCap),
	}
}

func (c *monitorClient) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			break
		default:
		}

		if err := c.runMonitor(ctx); err != nil {
			log.Err(err).Msg("MONITOR error")
		}
	}
}

func (c *monitorClient) runMonitor(ctx context.Context) error {
	timeout := time.Second * 5
	conn, err := net.DialTimeout("tcp", c.addr, timeout)
	if err != nil {
		log.Err(err).Str("address", c.addr).Msg("failed to connect to redis")
	}
	defer conn.Close()

	monitorCmd := "*1\r\nMONITOR\r\n"
	w := bufio.NewWriter(conn)
	_, err = w.WriteString(monitorCmd)
	if err != nil {
		log.Err(err).Str("address", c.addr).Msg("failed to connect to redis")
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
		c.data.add(line)
	}
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
