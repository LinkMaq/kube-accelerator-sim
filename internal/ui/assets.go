//go:build !kasim_measure_no_ui

package ui

import "embed"

// embeddedAssets is compiled directly into every released kasim CLI.
//
//go:embed static/*
var embeddedAssets embed.FS
