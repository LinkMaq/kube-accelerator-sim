package main

import (
	"context"
	"reflect"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func TestAllocateReturnsOnlyDeterministicTestMetadata(t *testing.T) {
	t.Parallel()

	plugin := newDevicePlugin([]string{"kasim-oracle-0", "kasim-oracle-1"})
	response, err := plugin.Allocate(
		context.Background(),
		&pluginapi.AllocateRequest{
			ContainerRequests: []*pluginapi.ContainerAllocateRequest{
				{DevicesIds: []string{"kasim-oracle-1"}},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ContainerResponses) != 1 {
		t.Fatalf("container responses = %d, want 1", len(response.ContainerResponses))
	}
	got := response.ContainerResponses[0]
	if !reflect.DeepEqual(
		got.Envs,
		map[string]string{"KASIM_ORACLE_DEVICE_IDS": "kasim-oracle-1"},
	) {
		t.Fatalf("allocation environment = %#v", got.Envs)
	}
	if len(got.Devices) != 0 || len(got.Mounts) != 0 ||
		len(got.CdiDevices) != 0 {
		t.Fatalf("test oracle exposed host artifacts: %#v", got)
	}
}

func TestAllocateRejectsUnknownAndUnhealthyDevices(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		prepare  func(*devicePlugin)
		deviceID string
	}{
		"unknown": {
			deviceID: "not-an-oracle-device",
			prepare:  func(*devicePlugin) {},
		},
		"unhealthy": {
			deviceID: "kasim-oracle-0",
			prepare: func(plugin *devicePlugin) {
				if err := plugin.setHealth(pluginapi.Unhealthy); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			plugin := newDevicePlugin([]string{"kasim-oracle-0"})
			test.prepare(plugin)
			_, err := plugin.Allocate(
				context.Background(),
				&pluginapi.AllocateRequest{
					ContainerRequests: []*pluginapi.ContainerAllocateRequest{
						{DevicesIds: []string{test.deviceID}},
					},
				},
			)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("Allocate error = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestPreferredAllocationPreservesRequiredDevicesDeterministically(t *testing.T) {
	t.Parallel()

	plugin := newDevicePlugin(
		[]string{"kasim-oracle-0", "kasim-oracle-1", "kasim-oracle-2"},
	)
	response, err := plugin.GetPreferredAllocation(
		context.Background(),
		&pluginapi.PreferredAllocationRequest{
			ContainerRequests: []*pluginapi.ContainerPreferredAllocationRequest{
				{
					AvailableDeviceIDs:   []string{"kasim-oracle-2", "kasim-oracle-0"},
					MustIncludeDeviceIDs: []string{"kasim-oracle-2"},
					AllocationSize:       2,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	got := response.ContainerResponses[0].DeviceIDs
	want := []string{"kasim-oracle-2", "kasim-oracle-0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("preferred devices = %#v, want %#v", got, want)
	}
}

func TestHealthAcceptsOnlyProtocolValues(t *testing.T) {
	t.Parallel()

	plugin := newDevicePlugin([]string{"kasim-oracle-0"})
	if err := plugin.setHealth(pluginapi.Unhealthy); err != nil {
		t.Fatal(err)
	}
	if got := plugin.snapshot()[0].Health; got != pluginapi.Unhealthy {
		t.Fatalf("device health = %q, want %q", got, pluginapi.Unhealthy)
	}
	if err := plugin.setHealth("degraded"); err == nil {
		t.Fatal("invalid protocol health unexpectedly succeeded")
	}
}
