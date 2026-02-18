package autotune

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFilePersister_PersistAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "changes.json")

	fp := NewFilePersister(path, 3, time.Second)

	changes := []ConfigChange{
		{
			Timestamp: time.Now(),
			Domain:    "limits",
			RuleName:  "rule_a",
			Field:     "max_cardinality",
			OldValue:  1000,
			NewValue:  1250,
			Action:    "increase",
			Reason:    "high utilization",
		},
	}

	if err := fp.Persist(context.Background(), changes); err != nil {
		t.Fatalf("persist failed: %v", err)
	}

	loaded, err := fp.LoadHistory(context.Background())
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if len(loaded) != 1 {
		t.Fatalf("expected 1 change, got %d", len(loaded))
	}
	if loaded[0].RuleName != "rule_a" {
		t.Errorf("expected rule_a, got %s", loaded[0].RuleName)
	}
	if loaded[0].NewValue != 1250 {
		t.Errorf("expected new value 1250, got %d", loaded[0].NewValue)
	}
}

func TestFilePersister_AppendMultiple(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "changes.json")

	fp := NewFilePersister(path, 3, time.Second)

	c1 := []ConfigChange{{RuleName: "rule_a", Action: "increase"}}
	c2 := []ConfigChange{{RuleName: "rule_b", Action: "decrease"}}

	if err := fp.Persist(context.Background(), c1); err != nil {
		t.Fatalf("persist 1 failed: %v", err)
	}
	if err := fp.Persist(context.Background(), c2); err != nil {
		t.Fatalf("persist 2 failed: %v", err)
	}

	loaded, err := fp.LoadHistory(context.Background())
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(loaded))
	}
}

func TestFilePersister_LoadEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	fp := NewFilePersister(path, 3, time.Second)
	loaded, err := fp.LoadHistory(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("expected empty, got %d", len(loaded))
	}
}

func TestFilePersister_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "dir", "changes.json")

	fp := NewFilePersister(path, 3, time.Second)
	changes := []ConfigChange{{RuleName: "test"}}

	if err := fp.Persist(context.Background(), changes); err != nil {
		t.Fatalf("persist failed: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("file was not created")
	}
}

func TestFilePersister_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "changes.json")

	fp := NewFilePersister(path, 3, time.Second)

	// Write initial data.
	if err := fp.Persist(context.Background(), []ConfigChange{{RuleName: "first"}}); err != nil {
		t.Fatalf("first persist failed: %v", err)
	}

	// Write again — should overwrite atomically.
	if err := fp.Persist(context.Background(), []ConfigChange{{RuleName: "second"}}); err != nil {
		t.Fatalf("second persist failed: %v", err)
	}

	loaded, _ := fp.LoadHistory(context.Background())
	if len(loaded) != 2 {
		t.Fatalf("expected 2 entries (first+second), got %d", len(loaded))
	}

	// Verify no temp files left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}
