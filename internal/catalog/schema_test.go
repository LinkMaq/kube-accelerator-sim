package catalog_test

import (
	"encoding/json"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/profiles"
)

func TestPublishedCatalogSchemaIsStrictValidJSON(t *testing.T) {
	t.Parallel()

	if !json.Valid(profiles.SchemaJSON) {
		t.Fatal("published profile schema is not valid JSON")
	}
	var schema struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           struct {
			SchemaVersion struct {
				Constant string `json:"const"`
			} `json:"schemaVersion"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(profiles.SchemaJSON, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties {
		t.Fatal("published profile schema permits unknown top-level fields")
	}
	if schema.Properties.SchemaVersion.Constant != "v1alpha1" {
		t.Fatalf(
			"schema version = %q",
			schema.Properties.SchemaVersion.Constant,
		)
	}
}
