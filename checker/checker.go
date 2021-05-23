package main

import (
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/doyoubi/undermoon-operator/checker/pkg"
)

func main() {
	c := pkg.NewCheckerClusterClient("localhost:5299", time.Second, true)
	v, err := c.MGet([]string{"key{1}", "k{1}"})
	if err != nil {
		log.Err(err).Msg("err")
		return
	}
	log.Info().Str("v", fmt.Sprintf("%+v", v)).Send()
}
