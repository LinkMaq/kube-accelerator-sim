package main

import (
	"testing"
)

const testRevision = "0123456789abcdef0123456789abcdef01234567"

func TestValidateScaleRequiresTwoSafeExactTrials(t *testing.T) {
	t.Parallel()

	first := scaleTestReceipt(1)
	second := scaleTestReceipt(2)
	if err := validateScale([]receipt{first, second}, testRevision); err != nil {
		t.Fatalf("validate exact scale receipts: %v", err)
	}

	second.Document["outcomes"].(map[string]any)["observedCountReduction"] = true
	if err := validateScale([]receipt{first, second}, testRevision); err == nil {
		t.Fatal("scale evidence accepted an observed count reduction")
	}
}

func TestValidateCommonRejectsCrossRevisionEvidence(t *testing.T) {
	t.Parallel()

	item := scaleTestReceipt(1)
	if err := validateCommon(
		item,
		scaleReceiptSchema,
		"ffffffffffffffffffffffffffffffffffffffff",
	); err == nil {
		t.Fatal("release evidence accepted another source revision")
	}
}

func scaleTestReceipt(trial int) receipt {
	thresholds := map[string]any{
		"applyReadySeconds":         float64(180),
		"observationP95Seconds":     float64(2),
		"healthLossSeconds":         float64(15),
		"healthRecoverySeconds":     float64(15),
		"workloadSeconds":           float64(60),
		"controllerRecoverySeconds": float64(120),
		"cleanupSeconds":            float64(120),
		"controlPlanePeakBytes":     float64(2 << 30),
	}
	measurements := map[string]any{}
	for field, value := range thresholds {
		measurements[field] = value.(float64) / 2
	}
	return receipt{
		Path: "scale.json",
		Document: map[string]any{
			"schemaVersion":  scaleReceiptSchema,
			"sourceRevision": testRevision,
			"checkedAt":      "2026-07-30T00:00:00Z",
			"trial":          float64(trial),
			"trialsRequired": float64(2),
			"result":         "passed",
			"scenario": map[string]any{
				"syntheticNodes":     float64(1000),
				"accelerators":       float64(8000),
				"representativePods": float64(100),
			},
			"thresholds":   thresholds,
			"measurements": measurements,
			"outcomes": map[string]any{
				"applyReady":             true,
				"observationP95":         true,
				"healthLoss":             true,
				"healthRecovery":         true,
				"workload":               true,
				"controllerRecovery":     true,
				"cleanup":                true,
				"apiErrors":              float64(0),
				"controllerErrors":       float64(0),
				"controllerCrashes":      float64(0),
				"ownedLiveObjects":       float64(0),
				"identityDrift":          false,
				"observedCountReduction": false,
			},
		},
	}
}
