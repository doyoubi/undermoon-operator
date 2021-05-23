package main

import (
	"context"

	"github.com/doyoubi/undermoon-operator/checker/pkg"
)

func main() {
	pkg.RunKvCheckerService(context.Background(), "localhost:5299")
}
