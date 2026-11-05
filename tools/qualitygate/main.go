package main

import (
	"flag"
	"fmt"
	"os"
	"unburger/internal/hygiene"
)

func main() {
	root := flag.String("root", ".", "project root")
	flag.Parse()
	fails := hygiene.Run(*root)
	if len(fails) > 0 {
		for _, e := range fails {
			fmt.Fprintln(os.Stderr, "FAIL:", e)
		}
		os.Exit(1)
	}
	fmt.Println("PASS: active docs, workspace layout, public-file hygiene and project marker")
}
