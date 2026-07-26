// Command cryptox-fixture seals one local test-fixture value with the real
// cryptox envelope implementation. It is intentionally narrow: the key is
// accepted only through CRYPTOX_KEY_V1, ciphertext is written as hex, and no
// decrypt operation exists. Production services do not invoke this command.
package main

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/herdifirdausss/seev/pkg/cryptox"
)

func main() {
	if len(os.Args) != 6 {
		fmt.Fprintln(os.Stderr, "usage: CRYPTOX_KEY_V1=<hex> cryptox-fixture <service> <table> <column> <row-id> <plaintext>")
		os.Exit(2)
	}
	key, err := hex.DecodeString(os.Getenv("CRYPTOX_KEY_V1"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cryptox-fixture: decode CRYPTOX_KEY_V1: %v\n", err)
		os.Exit(2)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cryptox-fixture: build ring: %v\n", err)
		os.Exit(2)
	}
	ciphertext, err := ring.Seal(cryptox.AAD{
		Service: os.Args[1],
		Table:   os.Args[2],
		Column:  os.Args[3],
		RowID:   os.Args[4],
	}, []byte(os.Args[5]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cryptox-fixture: seal: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(hex.EncodeToString(ciphertext))
}
