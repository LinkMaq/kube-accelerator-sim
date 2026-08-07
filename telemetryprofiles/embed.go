// Package telemetryprofiles embeds the immutable, source-backed metric
// contracts shipped with Kasim. Interpretation belongs to the Simulated
// Vendor Telemetry Module.
package telemetryprofiles

import _ "embed"

// CatalogJSON is the exact bundled telemetry catalog input.
//
//go:embed catalog.json
var CatalogJSON []byte
