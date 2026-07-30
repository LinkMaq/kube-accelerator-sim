package catalog_test

import (
	"strings"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

const validCustomCatalog = `{
  "schemaVersion": "v1alpha1",
  "revision": "lab-1",
  "profiles": [{
    "id": "lab",
    "displayName": "Lab Accelerator",
    "class": "custom",
    "evidence": [{
      "id": "contract",
      "grade": "C",
      "source": "https://example.com/contract",
      "revision": "1",
      "checkedAt": "2026-07-30"
    }],
    "contracts": [{
      "id": "device-plugin",
      "kind": "extended-resource",
      "providerScope": "lab-only",
      "fidelityModes": ["scheduling"],
      "resources": [{"alias":"accelerator","name":"lab.example.com/accelerator","unit":"device"}],
      "identitySignals": [],
      "capabilities": {
        "health": "not-public",
        "topology": "not-public",
        "sharing": "not-public",
        "partitioning": "not-public"
      },
      "evidenceRefs": ["contract"]
    }],
    "models": [{
      "id": "lab-accelerator",
      "displayName": "Lab Accelerator",
      "aliases": ["Lab Accelerator"],
      "lifecycle": "k8s-identified",
      "selectable": true,
      "contracts": ["device-plugin"],
      "resourceAliases": ["accelerator"],
      "evidenceRefs": ["contract"]
    }]
  }]
}`

func TestCustomCatalogRejectsUnknownFieldsAndNonCustomClasses(t *testing.T) {
	t.Parallel()

	const unknownField = `{
	  "schemaVersion": "v1alpha1",
	  "revision": "lab-1",
	  "unknown": true,
	  "profiles": []
	}`
	if _, err := catalog.LoadCustom(strings.NewReader(unknownField)); err == nil {
		t.Fatal("custom catalog with unknown field unexpectedly succeeded")
	}

	const bundledClass = `{
	  "schemaVersion": "v1alpha1",
	  "revision": "lab-1",
	  "profiles": [{
	    "id": "lab",
	    "displayName": "Lab",
	    "class": "verified",
	    "evidence": [{
	      "id": "contract",
	      "grade": "C",
	      "source": "https://example.com/contract",
	      "revision": "1",
	      "checkedAt": "2026-07-30"
	    }],
	    "contracts": [],
	    "models": []
	  }]
	}`
	if _, err := catalog.LoadCustom(strings.NewReader(bundledClass)); err == nil {
		t.Fatal("custom catalog claiming verified class unexpectedly succeeded")
	}
}

func TestCustomCatalogUsesTheSameResolutionAndDigestPath(t *testing.T) {
	t.Parallel()

	first, err := catalog.LoadCustom(strings.NewReader(validCustomCatalog))
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.LoadCustom(strings.NewReader(validCustomCatalog))
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest() != second.Digest() {
		t.Fatalf("same custom catalog produced different digests")
	}

	fidelity, err := domain.ParseFidelityMode("scheduling")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := first.Resolve(catalog.ResolveRequest{
		ProfileID:     "lab",
		ModelID:       "Lab Accelerator",
		ContractID:    "device-plugin",
		ResourceAlias: "accelerator",
		Fidelity:      fidelity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ModelID() != "lab-accelerator" ||
		resolved.ResourceName() != "lab.example.com/accelerator" ||
		resolved.ProfileDigest().String() == "" {
		t.Fatalf("unexpected custom resolution: %#v", resolved)
	}
}

func TestCatalogValidationRejectsAmbiguousOrUnevidencedContracts(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"unqualified resource": strings.Replace(
			validCustomCatalog,
			`"name":"lab.example.com/accelerator"`,
			`"name":"accelerator"`,
			1,
		),
		"unknown evidence": strings.Replace(
			validCustomCatalog,
			`"evidenceRefs": ["contract"]`,
			`"evidenceRefs": ["missing"]`,
			1,
		),
		"unknown model contract": strings.Replace(
			validCustomCatalog,
			`"contracts": ["device-plugin"]`,
			`"contracts": ["missing"]`,
			1,
		),
		"invalid capability state": strings.Replace(
			validCustomCatalog,
			`"health": "not-public"`,
			`"health": "false"`,
			1,
		),
		"invalid identity signal": strings.Replace(
			validCustomCatalog,
			`"identitySignals": []`,
			`"identitySignals": [{"kind":"metrics-label","key":"model"}]`,
			1,
		),
		"case-folded alias collision": strings.Replace(
			validCustomCatalog,
			`"aliases": ["Lab Accelerator"]`,
			`"aliases": ["Lab Accelerator", "lab accelerator"]`,
			1,
		),
		"alias shadows canonical ID": strings.Replace(
			validCustomCatalog,
			`"aliases": ["Lab Accelerator"]`,
			`"aliases": ["Lab Accelerator", "lab-accelerator"]`,
			1,
		),
		"floating catalog revision": strings.Replace(
			validCustomCatalog,
			`"revision": "lab-1"`,
			`"revision": ""`,
			1,
		),
	}

	for name, input := range tests {
		name := name
		input := input
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := catalog.LoadCustom(strings.NewReader(input)); err == nil {
				t.Fatal("invalid custom catalog unexpectedly succeeded")
			}
		})
	}
}
