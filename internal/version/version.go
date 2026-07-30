package version

import "fmt"

const (
	SchemaVersion        = "v1alpha1"
	CatalogVersion       = "unavailable"
	KubernetesFloor      = "1.30"
	KubernetesCeiling    = "1.36"
	developmentVersion   = "dev"
	unknownBuildMetadata = "unknown"
)

var (
	productVersion = developmentVersion
	sourceRevision = unknownBuildMetadata
	buildDate      = unknownBuildMetadata
)

// Info is the immutable build and compatibility result used by presentation.
type Info struct {
	Binary            string
	ProductVersion    string
	SourceRevision    string
	BuildDate         string
	SchemaVersion     string
	CatalogVersion    string
	KubernetesFloor   string
	KubernetesCeiling string
}

// Build returns build metadata with the exact loaded catalog revision.
func Build(binary, catalogVersion string) Info {
	return Info{
		Binary:            binary,
		ProductVersion:    productVersion,
		SourceRevision:    sourceRevision,
		BuildDate:         buildDate,
		SchemaVersion:     SchemaVersion,
		CatalogVersion:    catalogVersion,
		KubernetesFloor:   KubernetesFloor,
		KubernetesCeiling: KubernetesCeiling,
	}
}

func Human(binary string) string {
	info := Build(binary, CatalogVersion)
	return fmt.Sprintf(
		"%s %s (commit=%s, built=%s)\nschema=%s catalog=%s kubernetes=%s-%s\n",
		info.Binary,
		info.ProductVersion,
		info.SourceRevision,
		info.BuildDate,
		info.SchemaVersion,
		info.CatalogVersion,
		info.KubernetesFloor,
		info.KubernetesCeiling,
	)
}
