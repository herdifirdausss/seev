package sanctions

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/herdifirdausss/seev/internal/fraud/repository"
)

// NormalizeName folds case/diacritics and sorts tokens. It is intentionally
// conservative and deterministic; false positives are surfaced as monitor or
// refer decisions rather than auto-rejecting a customer.
func NormalizeName(value string) string {
	value = norm.NFD.String(strings.ToLower(strings.TrimSpace(value)))
	var b strings.Builder
	for _, r := range value {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	tokens := strings.Fields(b.String())
	sort.Strings(tokens)
	return strings.Join(tokens, " ")
}

type jsonEntity struct {
	ID         string `json:"id"`
	Caption    string `json:"caption"`
	Properties struct {
		Name      []string `json:"name"`
		BirthDate []string `json:"birthDate"`
	} `json:"properties"`
}

// LoadJSONL reads an OpenSanctions/FollowTheMoney-style JSONL export. The
// caller supplies the dataset version and a repository; no network access is
// performed here, keeping CI and the local fixture path offline.
func LoadJSONL(ctx context.Context, input io.Reader, repo repository.SanctionsRepository, source, version string) error {
	if repo == nil {
		return fmt.Errorf("sanctions: repository is nil")
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	entries := make([]repository.SanctionsEntry, 0, 1024)
	for scanner.Scan() {
		var entity jsonEntity
		if err := json.Unmarshal(scanner.Bytes(), &entity); err != nil {
			return fmt.Errorf("sanctions: decode JSONL: %w", err)
		}
		name := entity.Caption
		if len(entity.Properties.Name) > 0 && entity.Properties.Name[0] != "" {
			name = entity.Properties.Name[0]
		}
		name = NormalizeName(name)
		if entity.ID == "" || name == "" {
			continue
		}
		birth := ""
		if len(entity.Properties.BirthDate) > 0 {
			birth = entity.Properties.BirthDate[0]
		}
		entries = append(entries, repository.SanctionsEntry{ID: entity.ID, Source: source, NormalizedName: name, BirthDate: birth, DatasetVersion: version})
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("sanctions: read JSONL: %w", err)
	}
	return repo.ReplaceSanctions(ctx, entries)
}
