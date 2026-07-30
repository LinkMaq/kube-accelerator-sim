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

func Human(binary string) string {
	return fmt.Sprintf(
		"%s %s (commit=%s, built=%s)\nschema=%s catalog=%s kubernetes=%s-%s\n",
		binary,
		productVersion,
		sourceRevision,
		buildDate,
		SchemaVersion,
		CatalogVersion,
		KubernetesFloor,
		KubernetesCeiling,
	)
}
