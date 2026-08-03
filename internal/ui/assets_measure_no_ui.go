//go:build kasim_measure_no_ui

package ui

import "embed"

// The release builder uses this empty filesystem only to measure the
// compressed binary increase attributable to the embedded UI. Binaries built
// with kasim_measure_no_ui are never packaged or published.
var embeddedAssets embed.FS
