package migrationtest

import (
	"path/filepath"
	"testing"
)

func TestPostgresPaddleCheckoutMigration(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(root(t), "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
		if filepath.Base(f) == "000022_paddle_checkout.up.sql" {
			break
		}
	}
	testPostgresMigrations(t, names, "paddle.sql", "42P07", "", "")
}
