package telemetry

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

const sampleInterval = 15 * time.Second

type expositionFamily struct {
	metricType string
	help       string
	samples    []expositionSample
}

type expositionSample struct {
	labels map[string]string
	value  float64
}

func (module *Module) render(observation Observation, now time.Time) ([]byte, error) {
	if err := validateObservation(observation); err != nil {
		return nil, err
	}
	observation = sortedObservation(observation)
	families := make(map[string]*expositionFamily)
	addSample(families, "kasim_telemetry_catalog_info", "gauge",
		"Information about the immutable Kasim telemetry catalog.", map[string]string{
			"revision": module.contracts.revision,
			"digest":   module.contracts.digest,
		}, 1)
	addSample(families, "kasim_telemetry_source_up", "gauge",
		"Whether the latest Kasim telemetry source refresh succeeded.", nil, 1)
	addSample(families, "kasim_telemetry_render_errors_total", "counter",
		"Number of telemetry source or render failures since process start.", nil,
		float64(module.renderErrors.Load()))
	states := module.contracts.ProfileStates()
	profileIDs := make([]string, 0, len(states))
	for id := range states {
		profileIDs = append(profileIDs, id)
	}
	sort.Strings(profileIDs)
	for _, id := range profileIDs {
		state := states[id]
		available := 0.0
		if state == "verified" {
			available = 1
		}
		addSample(families, "kasim_telemetry_contract_available", "gauge",
			"Whether a source-backed built-in telemetry contract is enabled.",
			map[string]string{"profile": id, "state": state}, available)
	}
	for _, node := range observation.Nodes {
		addSample(families, "kasim_telemetry_node_info", "gauge",
			"Information about an exactly owned Kasim Synthetic Node represented by this endpoint.",
			map[string]string{
				"instance":     node.InstanceName,
				"instance_uid": node.InstanceUID,
				"node":         node.Name,
				"node_group":   node.Group,
			}, 1)
	}
	for _, device := range observation.Devices {
		profile, found := module.contracts.profile(device.ProfileID)
		if !found || profile.State != "verified" {
			state := "unavailable"
			if found {
				state = profile.State
			}
			addSample(families, "kasim_telemetry_device_contract_available", "gauge",
				"Whether one simulated device has an enabled source-backed native telemetry contract.",
				provenanceLabels(device, state), 0)
			continue
		}
		addSample(families, "kasim_telemetry_device_contract_available", "gauge",
			"Whether one simulated device has an enabled source-backed native telemetry contract.",
			provenanceLabels(device, profile.State), 1)
		labels := nativeLabels(profile, device)
		for key, value := range provenanceLabels(device, "") {
			if key != "state" {
				labels[key] = value
			}
		}
		limits := limitsFor(profile, device.ModelID)
		latent := deviceLatent(device, now)
		for _, metric := range profile.MetricFamily {
			value, supported := metricValue(metric, device, limits, latent, now)
			if !supported {
				continue
			}
			help := fmt.Sprintf(
				"Explicitly simulated Kasim value using the source-backed %s schema (%s).",
				metric.Name,
				metric.Unit,
			)
			addSample(families, metric.Name, metric.Type, help, labels, value)
		}
	}
	return encodeExposition(families), nil
}

func (module *Module) diagnosticExposition(up bool, reason string) []byte {
	families := make(map[string]*expositionFamily)
	value := 0.0
	if up {
		value = 1
	}
	addSample(families, "kasim_telemetry_catalog_info", "gauge",
		"Information about the immutable Kasim telemetry catalog.", map[string]string{
			"revision": module.contracts.revision,
			"digest":   module.contracts.digest,
		}, 1)
	addSample(families, "kasim_telemetry_source_up", "gauge",
		"Whether the latest Kasim telemetry source refresh succeeded.",
		map[string]string{"reason": reason}, value)
	addSample(families, "kasim_telemetry_render_errors_total", "counter",
		"Number of telemetry source or render failures since process start.", nil,
		float64(module.renderErrors.Load()))
	return encodeExposition(families)
}

func addSample(
	families map[string]*expositionFamily,
	name,
	metricType,
	help string,
	labels map[string]string,
	value float64,
) {
	family, found := families[name]
	if !found {
		family = &expositionFamily{metricType: metricType, help: help}
		families[name] = family
	}
	family.samples = append(family.samples, expositionSample{
		labels: cloneLabels(labels),
		value:  value,
	})
}

func provenanceLabels(device Device, state string) map[string]string {
	result := map[string]string{
		"kasim_device":       syntheticIdentity(device),
		"kasim_instance":     device.InstanceName,
		"kasim_instance_uid": device.InstanceUID,
		"kasim_model":        device.ModelID,
		"kasim_node":         device.NodeName,
		"kasim_node_group":   device.NodeGroup,
		"kasim_pool":         device.Pool,
		"kasim_profile":      device.ProfileID,
		"kasim_simulated":    "true",
		"kasim_value_model":  "correlated-v1",
		"node":               device.NodeName,
	}
	if state != "" {
		result["state"] = state
	}
	return result
}

func nativeLabels(profile profileRecord, device Device) map[string]string {
	result := make(map[string]string, len(profile.DeviceLabels))
	for _, label := range profile.DeviceLabels {
		switch label.ValueFrom {
		case "device-index":
			result[label.Name] = strconv.FormatUint(device.Ordinal, 10)
		case "device-name":
			result[label.Name] = "kasim" + strconv.FormatUint(device.Ordinal, 10)
		case "device-uuid":
			result[label.Name] = syntheticIdentity(device)
		case "empty":
			result[label.Name] = ""
		case "fixed":
			result[label.Name] = label.Value
		case "model-name":
			result[label.Name] = device.ModelID
		case "node-name":
			result[label.Name] = device.NodeName
		case "pci-bdf":
			result[label.Name] = syntheticPCIBDF(device)
		case "profile-name":
			result[label.Name] = profile.DisplayName
		case "serial":
			result[label.Name] = "kasim-serial-" + shortHash(deviceIdentityKey(device))
		}
	}
	return result
}

func limitsFor(profile profileRecord, modelID string) simulationLimits {
	limits := profile.Defaults
	for _, model := range profile.Models {
		if model.ID == modelID && model.MemoryMiB > 0 {
			limits.MemoryMiB = model.MemoryMiB
			break
		}
	}
	return limits
}

func metricValue(
	metric metricFamily,
	device Device,
	limits simulationLimits,
	latent float64,
	now time.Time,
) (float64, bool) {
	if !device.Healthy {
		if metric.Semantic == "health-enflame" {
			return 1, true
		}
		if metric.Semantic == "health-binary" {
			return 0, true
		}
		if metric.Semantic != "info" && metric.Semantic != "memory-total" {
			latent = 0
		}
	}
	memoryUsed := limits.MemoryMiB * (0.08 + 0.82*latent)
	switch metric.Semantic {
	case "utilization":
		return clamp(100*latent, 0, 100), true
	case "utilization-ratio":
		return clamp(latent, 0, 1), true
	case "memory-ratio":
		return clamp(0.08+0.82*latent, 0, 1), true
	case "memory-used":
		return convertMemory(memoryUsed, metric.Unit)
	case "memory-free":
		return convertMemory(math.Max(0, limits.MemoryMiB-memoryUsed), metric.Unit)
	case "memory-total":
		return convertMemory(limits.MemoryMiB, metric.Unit)
	case "power":
		return limits.IdlePowerW + (limits.MaxPowerW-limits.IdlePowerW)*latent, limits.MaxPowerW > 0
	case "temperature":
		return limits.IdleTempC + (limits.MaxTempC-limits.IdleTempC)*latent, limits.MaxTempC > 0
	case "clock-core":
		return limits.CoreClockMHz * (0.35 + 0.65*latent), limits.CoreClockMHz > 0
	case "clock-memory":
		return limits.MemoryClockMHz * (0.55 + 0.45*latent), limits.MemoryClockMHz > 0
	case "throughput-rx":
		return 32e9 * latent * (0.7 + 0.3*seedUnit(deviceIdentityKey(device)+metric.Name)), true
	case "throughput-tx":
		return 32e9 * latent * (0.65 + 0.35*seedUnit(deviceIdentityKey(device)+metric.Name)), true
	case "energy":
		ageBuckets := counterBuckets(now)
		power := limits.IdlePowerW + (limits.MaxPowerW-limits.IdlePowerW)*(0.25+0.5*seedUnit(deviceIdentityKey(device)))
		joules := ageBuckets * sampleInterval.Seconds() * power
		if metric.Unit == "millijoules" {
			joules *= 1000
		}
		return joules, limits.MaxPowerW > 0
	case "cycle-counter":
		rate := 3e8 + 1.2e9*seedUnit(deviceIdentityKey(device)+metric.Name)
		return counterBuckets(now) * sampleInterval.Seconds() * rate, true
	case "health-enflame":
		if device.Healthy {
			return 2, true
		}
		return 1, true
	case "health-binary":
		if device.Healthy {
			return 1, true
		}
		return 0, true
	case "traffic-rx-counter", "traffic-tx-counter":
		rate := 2.5e9 + 22.5e9*seedUnit(deviceIdentityKey(device)+metric.Semantic)
		return counterBuckets(now) * sampleInterval.Seconds() * rate, true
	case "packet-rx-counter", "packet-tx-counter":
		rate := 2e6 + 8e6*seedUnit(deviceIdentityKey(device)+metric.Semantic)
		return counterBuckets(now) * sampleInterval.Seconds() * rate, true
	case "link-rate":
		return 50e9, true
	case "ib-state":
		if device.Healthy {
			return 4, true
		}
		return 1, true
	case "ib-physical-state":
		if device.Healthy {
			return 5, true
		}
		return 3, true
	case "info":
		return 1, true
	default:
		return 0, false
	}
}

func counterBuckets(now time.Time) float64 {
	const epoch = int64(1767225600) // 2026-01-01T00:00:00Z
	seconds := now.Unix() - epoch
	if seconds < 0 {
		seconds = 0
	}
	return float64(seconds / int64(sampleInterval/time.Second))
}

func convertMemory(mebibytes float64, unit string) (float64, bool) {
	switch unit {
	case "MiB", "MB":
		return mebibytes, mebibytes > 0
	case "bytes":
		return mebibytes * 1024 * 1024, mebibytes > 0
	default:
		return 0, false
	}
}

func deviceLatent(device Device, now time.Time) float64 {
	bucket := now.Unix() / int64(sampleInterval/time.Second)
	key := deviceIdentityKey(device)
	phase := seedUnit(key+"phase") * 2 * math.Pi
	slow := 0.5 + 0.34*math.Sin(float64(bucket)/11+phase)
	fast := (seedUnit(fmt.Sprintf("%s:%d", key, bucket)) - 0.5) * 0.12
	return clamp(slow+fast, 0.02, 0.98)
}

func syntheticIdentity(device Device) string {
	return "kasim-" + shortHash(deviceIdentityKey(device))
}

func syntheticPCIBDF(device Device) string {
	hash := sha256.Sum256([]byte(deviceIdentityKey(device)))
	bus := 1 + int(hash[0])%0xfe
	return fmt.Sprintf("0000:%02x:00.%d", bus, device.Ordinal%8)
}

func deviceIdentityKey(device Device) string {
	return fmt.Sprintf("kasim.telemetry.v1|%s|%s|%s|%s|%d",
		device.InstanceUID, device.NodeName, device.Pool, device.ModelID, device.Ordinal)
}

func shortHash(value string) string {
	hash := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", hash[:12])
}

func seedUnit(value string) float64 {
	hash := sha256.Sum256([]byte(value))
	integer := binary.BigEndian.Uint64(hash[:8])
	return float64(integer>>11) / float64(uint64(1)<<53)
}

func clamp(value, minimum, maximum float64) float64 {
	return math.Max(minimum, math.Min(maximum, value))
}

func encodeExposition(families map[string]*expositionFamily) []byte {
	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	sort.Strings(names)
	var output strings.Builder
	for _, name := range names {
		family := families[name]
		output.WriteString("# HELP ")
		output.WriteString(name)
		output.WriteByte(' ')
		output.WriteString(escapeHelp(family.help))
		output.WriteByte('\n')
		output.WriteString("# TYPE ")
		output.WriteString(name)
		output.WriteByte(' ')
		output.WriteString(family.metricType)
		output.WriteByte('\n')
		for _, sample := range family.samples {
			output.WriteString(name)
			writeLabels(&output, sample.labels)
			output.WriteByte(' ')
			output.WriteString(strconv.FormatFloat(sample.value, 'f', -1, 64))
			output.WriteByte('\n')
		}
	}
	return []byte(output.String())
}

func writeLabels(output *strings.Builder, labels map[string]string) {
	if len(labels) == 0 {
		return
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	output.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			output.WriteByte(',')
		}
		output.WriteString(key)
		output.WriteString("=\"")
		output.WriteString(escapeLabel(labels[key]))
		output.WriteByte('"')
	}
	output.WriteByte('}')
}

func escapeHelp(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.ReplaceAll(value, "\n", "\\n")
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\n", "\\n")
	return strings.ReplaceAll(value, "\"", "\\\"")
}

func cloneLabels(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
