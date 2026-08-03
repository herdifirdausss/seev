package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/herdifirdausss/seev/analytics/reconciliation/internal/contract"
)

func main() {
	root := flag.String("root", "analytics", "analytics workspace root")
	flag.Parse()
	if errors := contract.Validate(*root); len(errors) != 0 {
		for _, err := range errors {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
	fmt.Println("analytics contracts, connector allowlists, and privacy boundary are valid")
}
