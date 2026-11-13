package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/yijiuba/dukou/cmd"
)

func main() {
	if err := cmd.Execute(os.Args[1:]); err != nil {
		if !errors.Is(err, cmd.ErrReported) {
			fmt.Fprintf(os.Stderr, "dukou: %v\n", err)
		}
		os.Exit(1)
	}
}
