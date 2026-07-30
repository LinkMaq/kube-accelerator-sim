package domain_test

import (
	"strings"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

func TestNameRequiresAStableDNSLabel(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"mixed-training-lab", "nvidia-workers", "pool-0", "a"} {
		name, err := domain.ParseName(input)
		if err != nil {
			t.Fatalf("ParseName(%q): %v", input, err)
		}
		if name.String() != input {
			t.Errorf("ParseName(%q) = %q", input, name)
		}
	}

	for _, input := range []string{
		"",
		"Uppercase",
		"-leading",
		"trailing-",
		"contains_underscore",
		strings.Repeat("a", 64),
	} {
		if _, err := domain.ParseName(input); err == nil {
			t.Errorf("ParseName(%q) unexpectedly succeeded", input)
		}
	}
}

func TestDigestRequiresCanonicalSHA256(t *testing.T) {
	t.Parallel()

	const value = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	digest, err := domain.ParseDigest(value)
	if err != nil {
		t.Fatalf("ParseDigest(%q): %v", value, err)
	}
	if digest.String() != value {
		t.Fatalf("ParseDigest(%q) = %q", value, digest)
	}

	for _, input := range []string{
		"",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"sha512:0123456789abcdef",
		"sha256:ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		"sha256:short",
	} {
		if _, err := domain.ParseDigest(input); err == nil {
			t.Errorf("ParseDigest(%q) unexpectedly succeeded", input)
		}
	}
}

func TestGenerationIsNonNegativeAndPreservesCreatePreconditionZero(t *testing.T) {
	t.Parallel()

	for _, input := range []int64{0, 1, 42} {
		generation, err := domain.NewGeneration(input)
		if err != nil {
			t.Fatalf("NewGeneration(%d): %v", input, err)
		}
		if generation.Value() != uint64(input) {
			t.Errorf("NewGeneration(%d) = %d", input, generation.Value())
		}
	}

	if _, err := domain.NewGeneration(-1); err == nil {
		t.Fatal("NewGeneration(-1) unexpectedly succeeded")
	}
}

func TestSimulatedIdentitiesAreDeterministicAndBackendNeutral(t *testing.T) {
	t.Parallel()

	instanceName, err := domain.ParseName("mixed-training-lab")
	if err != nil {
		t.Fatal(err)
	}
	uid, err := domain.ParseInstanceUID("6cb2dd6f-c608-4e79-aaf6-e3fa1287f73c")
	if err != nil {
		t.Fatal(err)
	}
	group, err := domain.ParseName("workers")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := domain.ParseName("training")
	if err != nil {
		t.Fatal(err)
	}

	firstNode, err := domain.SyntheticNodeName(instanceName, uid, group, 3)
	if err != nil {
		t.Fatal(err)
	}
	secondNode, err := domain.SyntheticNodeName(instanceName, uid, group, 3)
	if err != nil {
		t.Fatal(err)
	}
	if firstNode.String() == "" || firstNode != secondNode {
		t.Fatalf("node identity is not stable: %q != %q", firstNode, secondNode)
	}
	if len(firstNode.String()) > 63 {
		t.Fatalf("node name is too long: %q", firstNode)
	}

	firstDevice, err := domain.SimulatedDeviceID(uid, group, 3, pool, 7)
	if err != nil {
		t.Fatal(err)
	}
	secondDevice, err := domain.SimulatedDeviceID(uid, group, 3, pool, 7)
	if err != nil {
		t.Fatal(err)
	}
	otherDevice, err := domain.SimulatedDeviceID(uid, group, 3, pool, 8)
	if err != nil {
		t.Fatal(err)
	}
	if firstDevice == "" || firstDevice != secondDevice || firstDevice == otherDevice {
		t.Fatalf(
			"device identities are not stable and unique: %q, %q, %q",
			firstDevice,
			secondDevice,
			otherDevice,
		)
	}
	for _, forbidden := range []string{"kwok", "nvidia", "amd", "huawei"} {
		if strings.Contains(firstNode.String(), forbidden) ||
			strings.Contains(firstDevice, forbidden) {
			t.Fatalf("identity leaked implementation or vendor %q", forbidden)
		}
	}
}
