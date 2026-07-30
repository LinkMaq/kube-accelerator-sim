package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestArchiveDirectoryIsDeterministic(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(source, "nested", "receipt.json"),
		[]byte("{\"result\":\"passed\"}\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	epoch := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	first := filepath.Join(t.TempDir(), "first.tar.gz")
	second := filepath.Join(t.TempDir(), "second.tar.gz")
	if err := archiveDirectory(source, "evidence", first, epoch); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(
		filepath.Join(source, "nested", "receipt.json"),
		time.Now(),
		time.Now(),
	); err != nil {
		t.Fatal(err)
	}
	if err := archiveDirectory(source, "evidence", second, epoch); err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("same content and SourceDateEpoch produced different archives")
	}
}

func TestSemanticVersionContractRejectsLeadingZeroes(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"0.1.0", "1.2.3-rc.1", "1.2.3+build.7"} {
		if !semanticVersion.MatchString(value) {
			t.Errorf("valid Semantic Version %q was rejected", value)
		}
	}
	for _, value := range []string{"v1.2.3", "1.02.3", "dev", "1.2"} {
		if semanticVersion.MatchString(value) {
			t.Errorf("invalid Semantic Version %q was accepted", value)
		}
	}
}
