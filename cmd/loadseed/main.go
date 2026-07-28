// Command loadseed generates deterministic, synthetic B0 seed material. It
// is emit-only by design until an explicit owner-service/API seeding adapter is
// added; it never writes to an arbitrary database or production table.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var loadDatabase = regexp.MustCompile(`^seev_load_[a-z0-9_]+$`)

type manifest struct {
	SchemaVersion int               `json:"schema_version"`
	Kind          string            `json:"kind"`
	Seed          int64             `json:"seed"`
	Count         int               `json:"count"`
	Database      string            `json:"database"`
	Rows          int               `json:"rows"`
	Entries       int               `json:"entries"`
	SHA256        string            `json:"sha256"`
	SyntheticOnly bool              `json:"synthetic_only"`
	Parameters    map[string]string `json:"parameters"`
}

func main() {
	kind := flag.String("kind", "journey", "journey or ledger-size")
	seed := flag.Int64("seed", 53, "deterministic seed")
	count := flag.Int("count", 100, "number of logical records")
	database := flag.String("database", "seev_load_ledger", "validated disposable database name")
	out := flag.String("out", "", "output JSONL path under artifacts/load or /tmp")
	ack := flag.String("ack", "", "disposable-only acknowledgement")
	flag.Parse()
	if *ack != "disposable-only" {
		fail(fmt.Errorf("set -ack disposable-only"))
	}
	if *kind != "journey" && *kind != "ledger-size" {
		fail(fmt.Errorf("unsupported seed kind %q", *kind))
	}
	if *count < 1 || *count > 5_000_000 {
		fail(fmt.Errorf("count must be between 1 and 5000000"))
	}
	if !loadDatabase.MatchString(*database) {
		fail(fmt.Errorf("unsafe database name %q", *database))
	}
	if !safeOutput(*out) {
		fail(fmt.Errorf("output must be under artifacts/load or /tmp"))
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0750); err != nil {
		fail(err)
	}
	file, err := os.OpenFile(*out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		fail(err)
	}
	defer file.Close()
	hash := sha256.New()
	writer := io.MultiWriter(file, hash)
	buffer := bufio.NewWriterSize(writer, 64*1024)
	encoder := json.NewEncoder(buffer)
	entries := 0
	for index := 0; index < *count; index++ {
		var row any
		if *kind == "ledger-size" {
			row, entries = ledgerRow(*seed, index), entries+2
		} else {
			row = journeyRow(*seed, index)
		}
		if err := encoder.Encode(row); err != nil {
			fail(err)
		}
	}
	if err := buffer.Flush(); err != nil {
		fail(err)
	}
	sum := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	metadata := manifest{SchemaVersion: 1, Kind: *kind, Seed: *seed, Count: *count, Database: *database, Rows: *count, Entries: entries, SHA256: sum, SyntheticOnly: true, Parameters: map[string]string{"output": filepath.Base(*out)}}
	manifestPath := *out + ".manifest.json"
	body, _ := json.MarshalIndent(metadata, "", "  ")
	if err := os.WriteFile(manifestPath, append(body, '\n'), 0600); err != nil {
		fail(err)
	}
	fmt.Println(manifestPath)
}

func journeyRow(seed int64, index int) map[string]any {
	id := uuid.NewSHA1(uuid.Nil, []byte("journey:"+strconv.FormatInt(seed, 10)+":"+strconv.Itoa(index)))
	return map[string]any{"kind": "journey_user", "index": index, "user_id": id, "email": fmt.Sprintf("load-user-%06d@example.invalid", index), "kyc_level": 1, "synthetic": true}
}

func ledgerRow(seed int64, index int) map[string]any {
	txID := uuid.NewSHA1(uuid.Nil, []byte("ledger:"+strconv.FormatInt(seed, 10)+":"+strconv.Itoa(index)))
	debit := uuid.NewSHA1(uuid.Nil, []byte("account:debit:"+strconv.Itoa(index%1000)))
	credit := uuid.NewSHA1(uuid.Nil, []byte("account:credit:"+strconv.Itoa(index%1000)))
	amount := 1000 + int64(index%100)
	return map[string]any{"kind": "ledger_transaction", "index": index, "tx_id": txID, "currency": "IDR", "amount_minor": amount, "occurred_at": time.Date(2024, 1, 1, 0, 0, index%60, 0, time.UTC).Add(time.Duration(index/60) * time.Minute), "entries": []map[string]any{{"account_id": debit, "direction": "debit", "amount_minor": amount}, {"account_id": credit, "direction": "credit", "amount_minor": amount}}, "balanced": true, "synthetic": true}
}

func safeOutput(path string) bool {
	return strings.HasPrefix(filepath.Clean(path), "artifacts/load/") || strings.HasPrefix(filepath.Clean(path), "/tmp/")
}
func fail(err error) { fmt.Fprintln(os.Stderr, "loadseed:", err); os.Exit(1) }
