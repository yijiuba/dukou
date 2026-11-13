package main

import (
	"fmt"
	"os"

	"github.com/yijiuba/dukou/cmd"
)

func main() {
	if err := cmd.Execute(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "dukou: %v\n", err)
		os.Exit(1)
	}
}
