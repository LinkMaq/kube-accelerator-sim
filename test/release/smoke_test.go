package release_test

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestReleaseCLIArchive is opt-in because every release workflow row executes
// the archive native to that runner's operating system and architecture.
func TestReleaseCLIArchive(t *testing.T) {
	root := os.Getenv("KASIM_RELEASE_ARTIFACT_DIR")
	if root == "" {
		t.Skip("KASIM_RELEASE_ARTIFACT_DIR is unset")
	}
	version := os.Getenv("KASIM_RELEASE_VERSION")
	revision := os.Getenv("KASIM_RELEASE_REVISION")
	if version == "" || revision == "" {
		t.Fatal("release version and revision are required")
	}
	extension := ".tar.gz"
	if runtime.GOOS == "windows" {
		extension = ".zip"
	}
	name := fmt.Sprintf(
		"kasim_%s_%s_%s%s",
		version,
		runtime.GOOS,
		runtime.GOARCH,
		extension,
	)
	archivePath := filepath.Join(root, name)
	assertChecksum(t, filepath.Join(root, "checksums.txt"), name, archivePath)

	destination := t.TempDir()
	binary := extractArchive(t, archivePath, destination)
	command := exec.Command(binary, "version", "-o", "json")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("execute released CLI: %v\n%s", err, output)
	}
	var envelope struct {
		Status string `json:"status"`
		Result struct {
			ProductVersion    string `json:"productVersion"`
			SourceRevision    string `json:"sourceRevision"`
			KubernetesFloor   string `json:"kubernetesFloor"`
			KubernetesCeiling string `json:"kubernetesCeiling"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		t.Fatalf("decode version output: %v\n%s", err, output)
	}
	if envelope.Status != "success" ||
		envelope.Result.ProductVersion != version ||
		envelope.Result.SourceRevision != revision ||
		envelope.Result.KubernetesFloor != "1.30" ||
		envelope.Result.KubernetesCeiling != "1.36" {
		t.Fatalf("released CLI returned wrong receipt: %#v", envelope)
	}
}

func assertChecksum(t *testing.T, manifest, name, path string) {
	t.Helper()
	encoded, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	expected := ""
	for _, line := range strings.Split(string(encoded), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			expected = fields[0]
			break
		}
	}
	if expected == "" {
		t.Fatalf("checksums.txt has no entry for %s", name)
	}
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	digester := sha256.New()
	if _, err := io.Copy(digester, input); err != nil {
		t.Fatal(err)
	}
	actual := hex.EncodeToString(digester.Sum(nil))
	if actual != expected {
		t.Fatalf("%s SHA-256 = %s, want %s", name, actual, expected)
	}
}

func extractArchive(t *testing.T, path, destination string) string {
	t.Helper()
	binaryName := "kasim"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
		reader, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		if len(reader.File) != 1 || reader.File[0].Name != binaryName {
			t.Fatalf("unexpected ZIP entries in %s", path)
		}
		target := filepath.Join(destination, binaryName)
		copyZIPEntry(t, reader.File[0], target)
		return target
	}
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	compressed, err := gzip.NewReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	header, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != binaryName || strings.Contains(header.Name, "..") {
		t.Fatalf("unexpected tar entry %q", header.Name)
	}
	target := filepath.Join(destination, binaryName)
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, reader); err != nil {
		output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Next(); err != io.EOF {
		t.Fatalf("tar archive contains extra entries: %v", err)
	}
	return target
}

func copyZIPEntry(t *testing.T, entry *zip.File, target string) {
	t.Helper()
	input, err := entry.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}
