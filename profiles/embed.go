// Package profiles embeds the immutable source-backed catalog shipped with
// the product. Interpretation and validation belong to the Profile Catalog
// Module.
package profiles

import _ "embed"

// CatalogJSON is the exact bundled catalog input used to compute release
// digests.
//
//go:embed catalog.json
var CatalogJSON []byte

// SchemaJSON is the strict JSON Schema published for bundled and custom
// profile catalog inputs.
//
//go:embed schema.json
var SchemaJSON []byte
