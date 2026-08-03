// Command releasebuild creates the deterministic, platform-neutral portion of
// a kasim release. Registry publication, SBOM generation, attestations, and
// signing remain explicit workflow stages around this builder.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	releaseReceiptSchema    = "kasim.io/release-receipt/v1alpha1"
	releaseDependencySchema = "kasim.io/release-dependencies/v1alpha1"
	maxUIAssetRawBytes      = 256 << 10
	maxUIAssetGzipBytes     = 96 << 10
	maxUIBinaryDeltaBytes   = 1 << 20
)

var (
	semanticVersion = regexp.MustCompile(
		`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`,
	)
	fullRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type buildOptions struct {
	Version         string
	Revision        string
	BuildDate       string
	OutputDirectory string
	EvidenceDir     string
	SourceDateEpoch time.Time
}

type target struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type uiPlatformMeasurement struct {
	OS                         string `json:"os"`
	Arch                       string `json:"arch"`
	ReleasedCompressedBytes    int64  `json:"releasedCompressedBytes"`
	WithoutUICompressedBytes   int64  `json:"withoutUICompressedBytes"`
	CompressedBinaryDeltaBytes int64  `json:"compressedBinaryDeltaBytes"`
}

type uiPackageBudget struct {
	AssetRawBytes              int64                   `json:"assetRawBytes"`
	AssetGzipBytes             int64                   `json:"assetGzipBytes"`
	AssetRawLimitBytes         int64                   `json:"assetRawLimitBytes"`
	AssetGzipLimitBytes        int64                   `json:"assetGzipLimitBytes"`
	CompressedBinaryDeltaLimit int64                   `json:"compressedBinaryDeltaLimitBytes"`
	PlatformMeasurements       []uiPlatformMeasurement `json:"platforms"`
}

func (target target) archiveName(version string) string {
	extension := ".tar.gz"
	if target.OS == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf(
		"kasim_%s_%s_%s%s",
		version,
		target.OS,
		target.Arch,
		extension,
	)
}

var releaseTargets = []target{
	{OS: "linux", Arch: "amd64"},
	{OS: "linux", Arch: "arm64"},
	{OS: "darwin", Arch: "amd64"},
	{OS: "darwin", Arch: "arm64"},
	{OS: "windows", Arch: "amd64"},
}

type releaseInputs struct {
	SchemaVersion   string         `json:"schemaVersion"`
	ProductVersion  string         `json:"productVersion"`
	PublicSurfaces  map[string]any `json:"publicSurfaces"`
	GoVersion       string         `json:"goVersion"`
	ControllerImage struct {
		Platforms []string `json:"platforms"`
		Builder   string   `json:"builder"`
		Runtime   string   `json:"runtime"`
	} `json:"controllerImage"`
	Catalog       any `json:"catalog"`
	CatalogSchema any `json:"catalogSchema"`
	ProductCRD    any `json:"productCRD"`
	Compatibility any `json:"compatibility"`
	KWOK          any `json:"kwok"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "releasebuild:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("releasebuild", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var options buildOptions
	flags.StringVar(&options.Version, "version", "", "Semantic version without v")
	flags.StringVar(&options.Revision, "revision", "", "full source revision")
	flags.StringVar(&options.BuildDate, "build-date", "", "RFC3339 source date")
	flags.StringVar(&options.OutputDirectory, "output", "", "empty output directory")
	flags.StringVar(&options.EvidenceDir, "evidence-dir", "", "validated evidence directory")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %q", flags.Args())
	}
	if !semanticVersion.MatchString(options.Version) {
		return fmt.Errorf("version %q is not Semantic Versioning", options.Version)
	}
	if !fullRevision.MatchString(options.Revision) {
		return fmt.Errorf("revision %q must be a full lowercase Git SHA", options.Revision)
	}
	sourceDate, err := time.Parse(time.RFC3339, options.BuildDate)
	if err != nil {
		return fmt.Errorf("build-date must be RFC3339: %w", err)
	}
	options.SourceDateEpoch = sourceDate.UTC()
	if options.OutputDirectory == "" || options.EvidenceDir == "" {
		return errors.New("output and evidence-dir are required")
	}
	if err := requireEmptyDirectory(options.OutputDirectory); err != nil {
		return err
	}
	if err := requireDirectory(options.EvidenceDir); err != nil {
		return fmt.Errorf("evidence-dir: %w", err)
	}

	inputs, err := loadReleaseInputs()
	if err != nil {
		return err
	}
	if inputs.ProductVersion != options.Version {
		return fmt.Errorf(
			"release input productVersion %q does not match %q",
			inputs.ProductVersion,
			options.Version,
		)
	}
	if err := validateChartVersion(options.Version); err != nil {
		return err
	}

	temporary, err := os.MkdirTemp("", "kasim-release-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	budget, err := measureUIAssets()
	if err != nil {
		return err
	}

	artifacts := make([]string, 0, len(releaseTargets)+2)
	for _, buildTarget := range releaseTargets {
		name := buildTarget.archiveName(options.Version)
		measurement, err := buildCLIArchive(
			options,
			buildTarget,
			filepath.Join(temporary, buildTarget.OS+"-"+buildTarget.Arch),
			filepath.Join(options.OutputDirectory, name),
		)
		if err != nil {
			return err
		}
		budget.PlatformMeasurements = append(
			budget.PlatformMeasurements,
			measurement,
		)
		artifacts = append(artifacts, name)
	}
	chartName := "kasim-runtime-" + options.Version + ".tgz"
	if err := archiveDirectory(
		"charts/kasim-runtime",
		"kasim-runtime",
		filepath.Join(options.OutputDirectory, chartName),
		options.SourceDateEpoch,
	); err != nil {
		return fmt.Errorf("package chart: %w", err)
	}
	artifacts = append(artifacts, chartName)

	evidenceName := "kasim-release-evidence-" + options.Version + ".tar.gz"
	if err := archiveDirectory(
		options.EvidenceDir,
		"release-evidence",
		filepath.Join(options.OutputDirectory, evidenceName),
		options.SourceDateEpoch,
	); err != nil {
		return fmt.Errorf("package release evidence: %w", err)
	}
	artifacts = append(artifacts, evidenceName)
	sort.Strings(artifacts)

	if err := writeDependencyLock(options, inputs); err != nil {
		return err
	}
	return writeReleaseReceipt(options, inputs, artifacts, budget)
}

func requireEmptyDirectory(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read output directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("output directory %q must be empty", path)
	}
	return nil
}

func requireDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", path)
	}
	return nil
}

func loadReleaseInputs() (releaseInputs, error) {
	encoded, err := os.ReadFile("release/inputs.json")
	if err != nil {
		return releaseInputs{}, fmt.Errorf("read release inputs: %w", err)
	}
	var inputs releaseInputs
	if err := json.Unmarshal(encoded, &inputs); err != nil {
		return releaseInputs{}, fmt.Errorf("decode release inputs: %w", err)
	}
	if inputs.SchemaVersion == "" || len(inputs.PublicSurfaces) == 0 {
		return releaseInputs{}, errors.New("release inputs lack schema or public surfaces")
	}
	return inputs, nil
}

func validateChartVersion(version string) error {
	encoded, err := os.ReadFile("charts/kasim-runtime/Chart.yaml")
	if err != nil {
		return err
	}
	var chart struct {
		Version    string `yaml:"version"`
		AppVersion string `yaml:"appVersion"`
	}
	if err := yaml.Unmarshal(encoded, &chart); err != nil {
		return fmt.Errorf("decode Chart.yaml: %w", err)
	}
	if chart.Version != version || chart.AppVersion != version {
		return fmt.Errorf(
			"chart version/appVersion %q/%q do not match %q",
			chart.Version,
			chart.AppVersion,
			version,
		)
	}
	return nil
}

func buildCLIArchive(
	options buildOptions,
	buildTarget target,
	stagingDirectory,
	archivePath string,
) (uiPlatformMeasurement, error) {
	if err := os.MkdirAll(stagingDirectory, 0o755); err != nil {
		return uiPlatformMeasurement{}, err
	}
	binaryName := "kasim"
	if buildTarget.OS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(stagingDirectory, binaryName)
	measurementPath := filepath.Join(
		stagingDirectory,
		"kasim-measure-without-ui"+filepath.Ext(binaryName),
	)
	versionPackage := "github.com/LinkMaq/kube-accelerator-sim/internal/version"
	linkerFlags := strings.Join([]string{
		"-s",
		"-w",
		"-buildid=",
		"-X", versionPackage + ".productVersion=" + options.Version,
		"-X", versionPackage + ".sourceRevision=" + options.Revision,
		"-X", versionPackage + ".buildDate=" + options.BuildDate,
	}, " ")
	if err := buildCLI(
		options,
		buildTarget,
		linkerFlags,
		binaryPath,
		"",
	); err != nil {
		return uiPlatformMeasurement{}, err
	}
	if err := buildCLI(
		options,
		buildTarget,
		linkerFlags,
		measurementPath,
		"kasim_measure_no_ui",
	); err != nil {
		return uiPlatformMeasurement{}, err
	}
	releasedCompressed, err := compressedFileSize(binaryPath)
	if err != nil {
		return uiPlatformMeasurement{}, err
	}
	withoutUICompressed, err := compressedFileSize(measurementPath)
	if err != nil {
		return uiPlatformMeasurement{}, err
	}
	delta := releasedCompressed - withoutUICompressed
	if delta > maxUIBinaryDeltaBytes {
		return uiPlatformMeasurement{}, fmt.Errorf(
			"%s/%s compressed UI binary delta %d exceeds %d bytes",
			buildTarget.OS,
			buildTarget.Arch,
			delta,
			maxUIBinaryDeltaBytes,
		)
	}
	measurement := uiPlatformMeasurement{
		OS:                         buildTarget.OS,
		Arch:                       buildTarget.Arch,
		ReleasedCompressedBytes:    releasedCompressed,
		WithoutUICompressedBytes:   withoutUICompressed,
		CompressedBinaryDeltaBytes: delta,
	}
	if buildTarget.OS == "windows" {
		return measurement, archiveZIP(
			binaryPath,
			binaryName,
			archivePath,
			options.SourceDateEpoch,
		)
	}
	return measurement, archiveSingleFile(
		binaryPath,
		binaryName,
		archivePath,
		options.SourceDateEpoch,
	)
}

func buildCLI(
	options buildOptions,
	buildTarget target,
	linkerFlags,
	outputPath,
	buildTags string,
) error {
	arguments := []string{
		"build",
		"-trimpath",
		"-ldflags=" + linkerFlags,
	}
	if buildTags != "" {
		arguments = append(arguments, "-tags="+buildTags)
	}
	arguments = append(arguments, "-o", outputPath, "./cmd/kasim")
	command := exec.Command("go", arguments...)
	command.Env = append(
		os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+buildTarget.OS,
		"GOARCH="+buildTarget.Arch,
	)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf(
			"build %s/%s CLI (tags %q): %w\n%s",
			buildTarget.OS,
			buildTarget.Arch,
			buildTags,
			err,
			output,
		)
	}
	return nil
}

func measureUIAssets() (uiPackageBudget, error) {
	paths := []string{
		"internal/ui/static/app.css",
		"internal/ui/static/app.js",
		"internal/ui/static/index.html",
	}
	var raw bytes.Buffer
	for _, path := range paths {
		encoded, err := os.ReadFile(path)
		if err != nil {
			return uiPackageBudget{}, err
		}
		raw.WriteString(path)
		raw.WriteByte(0)
		raw.Write(encoded)
	}
	compressed, err := compressedBytesSize(raw.Bytes())
	if err != nil {
		return uiPackageBudget{}, err
	}
	budget := uiPackageBudget{
		AssetRawBytes:              int64(raw.Len()),
		AssetGzipBytes:             compressed,
		AssetRawLimitBytes:         maxUIAssetRawBytes,
		AssetGzipLimitBytes:        maxUIAssetGzipBytes,
		CompressedBinaryDeltaLimit: maxUIBinaryDeltaBytes,
	}
	if budget.AssetRawBytes > maxUIAssetRawBytes {
		return uiPackageBudget{}, fmt.Errorf(
			"embedded UI raw bytes %d exceed %d",
			budget.AssetRawBytes,
			maxUIAssetRawBytes,
		)
	}
	if budget.AssetGzipBytes > maxUIAssetGzipBytes {
		return uiPackageBudget{}, fmt.Errorf(
			"embedded UI gzip bytes %d exceed %d",
			budget.AssetGzipBytes,
			maxUIAssetGzipBytes,
		)
	}
	return budget, nil
}

type countingWriter struct{ bytes int64 }

func (writer *countingWriter) Write(encoded []byte) (int, error) {
	writer.bytes += int64(len(encoded))
	return len(encoded), nil
}

func compressedBytesSize(encoded []byte) (int64, error) {
	counter := &countingWriter{}
	compressor, err := gzip.NewWriterLevel(counter, gzip.BestCompression)
	if err != nil {
		return 0, err
	}
	compressor.Header.ModTime = time.Unix(0, 0).UTC()
	compressor.Header.OS = 255
	if _, err := compressor.Write(encoded); err != nil {
		return 0, err
	}
	if err := compressor.Close(); err != nil {
		return 0, err
	}
	return counter.bytes, nil
}

func compressedFileSize(path string) (int64, error) {
	input, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer input.Close()
	counter := &countingWriter{}
	compressor, err := gzip.NewWriterLevel(counter, gzip.BestCompression)
	if err != nil {
		return 0, err
	}
	compressor.Header.ModTime = time.Unix(0, 0).UTC()
	compressor.Header.OS = 255
	if _, err := io.Copy(compressor, input); err != nil {
		return 0, err
	}
	if err := compressor.Close(); err != nil {
		return 0, err
	}
	return counter.bytes, nil
}

func archiveSingleFile(source, name, target string, epoch time.Time) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	output, tarWriter, closeArchive, err := newTarGzip(target, epoch)
	if err != nil {
		return err
	}
	defer output.Close()
	defer closeArchive()
	if err := tarWriter.WriteHeader(deterministicHeader(name, info.Size(), 0o755, epoch)); err != nil {
		return err
	}
	if _, err := io.Copy(tarWriter, input); err != nil {
		return err
	}
	return closeArchive()
}

func archiveZIP(source, name, target string, epoch time.Time) error {
	encoded, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(output)
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0o755)
	header.SetModTime(epoch)
	entry, err := writer.CreateHeader(header)
	if err == nil {
		_, err = entry.Write(encoded)
	}
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	return err
}

func archiveDirectory(sourceRoot, archiveRoot, target string, epoch time.Time) error {
	paths := make([]string, 0)
	if err := filepath.WalkDir(sourceRoot, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if path == sourceRoot {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("release archive rejects symlink %s", path)
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(paths)
	output, tarWriter, closeArchive, err := newTarGzip(target, epoch)
	if err != nil {
		return err
	}
	defer output.Close()
	defer closeArchive()
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(filepath.Join(archiveRoot, relative))
		if info.IsDir() {
			if err := tarWriter.WriteHeader(
				deterministicHeader(name+"/", 0, 0o755, epoch),
			); err != nil {
				return err
			}
			continue
		}
		mode := int64(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		if err := tarWriter.WriteHeader(
			deterministicHeader(name, info.Size(), mode, epoch),
		); err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tarWriter, input)
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return closeArchive()
}

func newTarGzip(
	path string,
	epoch time.Time,
) (*os.File, *tar.Writer, func() error, error) {
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, nil, err
	}
	compressor, err := gzip.NewWriterLevel(output, gzip.BestCompression)
	if err != nil {
		output.Close()
		return nil, nil, nil, err
	}
	compressor.Header.ModTime = epoch
	compressor.Header.OS = 255
	tarWriter := tar.NewWriter(compressor)
	closed := false
	closeArchive := func() error {
		if closed {
			return nil
		}
		closed = true
		if err := tarWriter.Close(); err != nil {
			return err
		}
		return compressor.Close()
	}
	return output, tarWriter, closeArchive, nil
}

func deterministicHeader(name string, size, mode int64, epoch time.Time) *tar.Header {
	typeFlag := byte(tar.TypeReg)
	if strings.HasSuffix(name, "/") {
		typeFlag = tar.TypeDir
		size = 0
	}
	return &tar.Header{
		Name:       filepath.ToSlash(name),
		Mode:       mode,
		Size:       size,
		ModTime:    epoch,
		AccessTime: epoch,
		ChangeTime: epoch,
		Typeflag:   typeFlag,
		Format:     tar.FormatPAX,
	}
}

func writeDependencyLock(options buildOptions, inputs releaseInputs) error {
	goVersion, err := commandText("go", "version")
	if err != nil {
		return err
	}
	files := []string{
		"go.mod",
		"go.sum",
		"release/inputs.json",
		"release/compatibility-lock.json",
		"charts/kasim-runtime/Chart.yaml",
		"charts/kasim-runtime/values.schema.json",
	}
	hashes := make(map[string]string, len(files))
	for _, path := range files {
		digest, err := fileSHA256(path)
		if err != nil {
			return err
		}
		hashes[path] = digest
	}
	document := map[string]any{
		"schemaVersion":   releaseDependencySchema,
		"productVersion":  options.Version,
		"sourceRevision":  options.Revision,
		"buildDate":       options.BuildDate,
		"goVersion":       goVersion,
		"inputsGoVersion": inputs.GoVersion,
		"files":           hashes,
		"controllerImageBases": map[string]string{
			"builder": inputs.ControllerImage.Builder,
			"runtime": inputs.ControllerImage.Runtime,
		},
		"kwok": inputs.KWOK,
	}
	return writeJSON(
		filepath.Join(options.OutputDirectory, "release-dependencies.json"),
		document,
	)
}

func writeReleaseReceipt(
	options buildOptions,
	inputs releaseInputs,
	artifacts []string,
	uiBudget uiPackageBudget,
) error {
	cliArchives := make([]string, 0, len(releaseTargets))
	for _, buildTarget := range releaseTargets {
		cliArchives = append(cliArchives, buildTarget.archiveName(options.Version))
	}
	document := map[string]any{
		"schemaVersion":   releaseReceiptSchema,
		"productVersion":  options.Version,
		"sourceRevision":  options.Revision,
		"buildDate":       options.BuildDate,
		"publicSurfaces":  inputs.PublicSurfaces,
		"catalog":         inputs.Catalog,
		"catalogSchema":   inputs.CatalogSchema,
		"productCRD":      inputs.ProductCRD,
		"compatibility":   inputs.Compatibility,
		"kwok":            inputs.KWOK,
		"cliArchives":     cliArchives,
		"artifacts":       artifacts,
		"uiPackageBudget": uiBudget,
		"controllerImage": map[string]any{
			"reference": "ghcr.io/linkmaq/kube-accelerator-sim-controller:" + options.Version,
			"platforms": inputs.ControllerImage.Platforms,
		},
		"chart": map[string]string{
			"archive": "kasim-runtime-" + options.Version + ".tgz",
			"oci":     "oci://ghcr.io/linkmaq/charts/kasim-runtime:" + options.Version,
		},
		"dependencyLock":  "release-dependencies.json",
		"evidenceArchive": "kasim-release-evidence-" + options.Version + ".tar.gz",
		"signingPolicy": map[string]any{
			"releaseAssets": "GitHub artifact attestation over checksums",
			"ociArtifacts":  "Sigstore keyless OIDC signing",
		},
	}
	return writeJSON(
		filepath.Join(options.OutputDirectory, "release-receipt.json"),
		document,
	)
}

func writeJSON(path string, document any) error {
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(path, encoded, 0o644)
}

func fileSHA256(path string) (string, error) {
	input, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer input.Close()
	digester := sha256.New()
	if _, err := io.Copy(digester, input); err != nil {
		return "", err
	}
	return hex.EncodeToString(digester.Sum(nil)), nil
}

func commandText(name string, arguments ...string) (string, error) {
	command := exec.Command(name, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %w\n%s", command.Args, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}
