package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

type devicePlugin struct {
	pluginapi.UnimplementedDevicePluginServer

	mu       sync.RWMutex
	devices  []string
	health   string
	watchers map[chan struct{}]struct{}
	logger   *log.Logger
}

func newDevicePlugin(deviceIDs []string) *devicePlugin {
	return newDevicePluginWithLogger(deviceIDs, log.New(io.Discard, "", 0))
}

func newDevicePluginWithLogger(
	deviceIDs []string,
	logger *log.Logger,
) *devicePlugin {
	devices := append([]string(nil), deviceIDs...)
	sort.Strings(devices)
	return &devicePlugin{
		devices:  devices,
		health:   pluginapi.Healthy,
		watchers: make(map[chan struct{}]struct{}),
		logger:   logger,
	}
}

func (plugin *devicePlugin) GetDevicePluginOptions(
	context.Context,
	*pluginapi.Empty,
) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{
		GetPreferredAllocationAvailable: true,
	}, nil
}

func (plugin *devicePlugin) ListAndWatch(
	_ *pluginapi.Empty,
	stream grpc.ServerStreamingServer[pluginapi.ListAndWatchResponse],
) error {
	updates := make(chan struct{}, 1)
	plugin.mu.Lock()
	plugin.watchers[updates] = struct{}{}
	plugin.mu.Unlock()
	defer func() {
		plugin.mu.Lock()
		delete(plugin.watchers, updates)
		plugin.mu.Unlock()
	}()

	send := func() error {
		return stream.Send(&pluginapi.ListAndWatchResponse{
			Devices: plugin.snapshot(),
		})
	}
	if err := send(); err != nil {
		return err
	}
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-updates:
			if err := send(); err != nil {
				return err
			}
		}
	}
}

func (plugin *devicePlugin) GetPreferredAllocation(
	_ context.Context,
	request *pluginapi.PreferredAllocationRequest,
) (*pluginapi.PreferredAllocationResponse, error) {
	response := &pluginapi.PreferredAllocationResponse{}
	for _, container := range request.GetContainerRequests() {
		if container.GetAllocationSize() < 0 {
			return nil, status.Error(
				codes.InvalidArgument,
				"allocation size must not be negative",
			)
		}
		want := int(container.GetAllocationSize())
		selected := append(
			[]string(nil),
			container.GetMustIncludeDeviceIDs()...,
		)
		seen := make(map[string]struct{}, len(selected))
		for _, deviceID := range selected {
			seen[deviceID] = struct{}{}
		}
		available := append(
			[]string(nil),
			container.GetAvailableDeviceIDs()...,
		)
		sort.Strings(available)
		for _, deviceID := range available {
			if len(selected) >= want {
				break
			}
			if _, exists := seen[deviceID]; exists {
				continue
			}
			selected = append(selected, deviceID)
			seen[deviceID] = struct{}{}
		}
		if len(selected) != want {
			return nil, status.Errorf(
				codes.InvalidArgument,
				"only %d devices available for allocation size %d",
				len(selected),
				want,
			)
		}
		response.ContainerResponses = append(
			response.ContainerResponses,
			&pluginapi.ContainerPreferredAllocationResponse{
				DeviceIDs: selected,
			},
		)
	}
	return response, nil
}

func (plugin *devicePlugin) Allocate(
	_ context.Context,
	request *pluginapi.AllocateRequest,
) (*pluginapi.AllocateResponse, error) {
	health := plugin.currentHealth()
	known := make(map[string]struct{}, len(plugin.devices))
	for _, deviceID := range plugin.devices {
		known[deviceID] = struct{}{}
	}

	response := &pluginapi.AllocateResponse{}
	for _, container := range request.GetContainerRequests() {
		deviceIDs := append([]string(nil), container.GetDevicesIds()...)
		for _, deviceID := range deviceIDs {
			if _, exists := known[deviceID]; !exists {
				return nil, status.Errorf(
					codes.InvalidArgument,
					"unknown oracle device %q",
					deviceID,
				)
			}
			if health != pluginapi.Healthy {
				return nil, status.Errorf(
					codes.InvalidArgument,
					"oracle device %q is %s",
					deviceID,
					health,
				)
			}
		}
		response.ContainerResponses = append(
			response.ContainerResponses,
			&pluginapi.ContainerAllocateResponse{
				Envs: map[string]string{
					"KASIM_ORACLE_DEVICE_IDS": strings.Join(deviceIDs, ","),
				},
			},
		)
		plugin.logger.Printf(
			`{"event":"allocation","deviceIDs":%q}`,
			strings.Join(deviceIDs, ","),
		)
	}
	return response, nil
}

func (plugin *devicePlugin) PreStartContainer(
	context.Context,
	*pluginapi.PreStartContainerRequest,
) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

func (plugin *devicePlugin) setHealth(health string) error {
	if health != pluginapi.Healthy && health != pluginapi.Unhealthy {
		return fmt.Errorf(
			"health must be %q or %q",
			pluginapi.Healthy,
			pluginapi.Unhealthy,
		)
	}

	plugin.mu.Lock()
	if plugin.health == health {
		plugin.mu.Unlock()
		return nil
	}
	plugin.health = health
	for watcher := range plugin.watchers {
		select {
		case watcher <- struct{}{}:
		default:
		}
	}
	plugin.mu.Unlock()
	plugin.logger.Printf(`{"event":"health-transition","health":%q}`, health)
	return nil
}

func (plugin *devicePlugin) snapshot() []*pluginapi.Device {
	plugin.mu.RLock()
	defer plugin.mu.RUnlock()

	devices := make([]*pluginapi.Device, 0, len(plugin.devices))
	for _, deviceID := range plugin.devices {
		devices = append(devices, &pluginapi.Device{
			ID:     deviceID,
			Health: plugin.health,
		})
	}
	return devices
}

func (plugin *devicePlugin) currentHealth() string {
	plugin.mu.RLock()
	defer plugin.mu.RUnlock()
	return plugin.health
}
