package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"k8s.io/klog/v2"

	"github.com/doyoubi/undermoon-operator/scheduler/pkg"
)

func main() {
	klog.V(0).Info("starting undermoon-scheduler")
	rand.Seed(time.Now().UTC().UnixNano())

	cmd := pkg.Register()
	if err := cmd.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
