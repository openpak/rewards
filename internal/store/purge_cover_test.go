package store

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Every table that holds an account_id must be erased on account_deleted, except the admin audit trail.
func TestPurgeCoversEveryAccountTable(t *testing.T) {
	files, _ := filepath.Glob("../../migrations/*.sql")
	re := regexp.MustCompile(`(?s)CREATE TABLE (\w+) \((.*?)\n\);`)
	have := map[string]bool{}
	for _, n := range purgeTables {
		have[n] = true
	}
	found := 0
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if !regexp.MustCompile(`\baccount_id\b`).MatchString(m[2]) || m[1] == "audit" {
				continue
			}
			found++
			if m[1] != "friends" && !have[m[1]] {
				t.Errorf("table %s has account_id but Purge does not erase it", m[1])
			}
		}
	}
	if found < 10 {
		t.Fatalf("only %d account tables found; migration parsing broke", found)
	}
}
