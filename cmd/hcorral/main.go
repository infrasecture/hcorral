package main

import (
	"os"

	"github.com/infrasecture/hcorral/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:], app.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}))
}
