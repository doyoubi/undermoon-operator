package main

import (
	"context"
	"os"

	"github.com/jessevdk/go-flags"
	"github.com/rs/zerolog/log"

	"github.com/doyoubi/undermoon-operator/checker/pkg"
)

type options struct {
	Address string `short:"a" long:"address" description:"A Redis address of the cluster"`
	OPS     int64  `long:"ops" description:"Commands sent per seconds"`
}

func main() {
	var opts options
	_, err := flags.Parse(&opts)
	if err != nil {
		log.Err(err).Msg("failed to parse flags")
		os.Exit(1)
	}

	pkg.RunKvCheckerService(context.Background(), opts.Address, opts.OPS)
}
