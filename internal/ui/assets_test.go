package ui

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"testing"
)

func TestEmbeddedAssetsStayInsideReleaseBudget(t *testing.T) {
	t.Parallel()

	var raw bytes.Buffer
	err := fs.WalkDir(embeddedAssets, "static", func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		encoded, err := fs.ReadFile(embeddedAssets, path)
		if err != nil {
			return err
		}
		raw.WriteString(path)
		raw.WriteByte(0)
		raw.Write(encoded)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, limit := raw.Len(), 256<<10; got > limit {
		t.Fatalf("embedded UI raw bytes = %d, limit %d", got, limit)
	}

	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if got, limit := compressed.Len(), 96<<10; got > limit {
		t.Fatalf("embedded UI gzip bytes = %d, limit %d", got, limit)
	}
}
