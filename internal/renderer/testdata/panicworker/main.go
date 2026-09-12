package main

import (
	"io"
	"os"
)

func main() {
	if os.Getenv("TRONBYT_RENDER_WORKER") != "1" {
		os.Exit(2)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	panic("simulated render worker panic")
}
