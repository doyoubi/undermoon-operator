package main

import (
	"context"
	"os"

	"github.com/jessevdk/go-flags"
	"github.com/rs/zerolog/log"

	"github.com/doyoubi/undermoon-operator/checker/pkg"
)

type options struct {
	Address          string   `short:"a" long:"address" description:"A Redis addresses of the cluster"`
	MonitorAddresses []string `short:"m" long:"monitor-address" description:"Redis addresses for MONITOR command"`
	OPS              int64    `long:"ops" description:"Commands sent per seconds"`
	MonitorBuf       int      `long:"monitor-buf" description:"MONITOR command buffer size, 0 to disable"`
	BlockOnError     bool     `long:"block-on-error" description:"Do not exit the process on error. It's mainly for running in container.'"`
}

func main() {
	var opts options
	_, err := flags.Parse(&opts)
	if err != nil {
		log.Err(err).Msg("failed to parse flags")
		os.Exit(1)
	}

	pkg.RunKvCheckerService(context.Background(), opts.Address, opts.OPS, opts.MonitorAddresses, opts.MonitorBuf, opts.BlockOnError)
}
