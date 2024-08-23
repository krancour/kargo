package main

import (
	"os"

	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	_ "github.com/gogo/protobuf/gogoproto"
)

func main() {
	ctx := signals.SetupSignalHandler()
	if err := Execute(ctx); err != nil {
		os.Exit(1)
	}
}
