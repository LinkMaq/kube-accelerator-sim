package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
)

const maximumNameLength = 63

var dnsLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
var sha256DigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var instanceUIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

// Name is a stable DNS-label identity used by Scenario-owned values.
type Name struct {
	value string
}

// ParseName validates the portable naming boundary before object rendering.
func ParseName(value string) (Name, error) {
	if len(value) == 0 || len(value) > maximumNameLength || !dnsLabelPattern.MatchString(value) {
		return Name{}, fmt.Errorf("%q is not a valid stable DNS label", value)
	}
	return Name{value: value}, nil
}

func (name Name) String() string {
	return name.value
}

// Digest is a canonical lowercase SHA-256 content identity.
type Digest struct {
	value string
}

// ParseDigest accepts only a complete sha256:<lowercase-hex> value.
func ParseDigest(value string) (Digest, error) {
	if !sha256DigestPattern.MatchString(value) {
		return Digest{}, fmt.Errorf("%q is not a canonical SHA-256 digest", value)
	}
	return Digest{value: value}, nil
}

func (digest Digest) String() string {
	return digest.value
}

// Generation is a non-negative desired-state sequence; zero is the create
// precondition.
type Generation struct {
	value uint64
}

// NewGeneration rejects negative values before crossing a transport seam.
func NewGeneration(value int64) (Generation, error) {
	if value < 0 {
		return Generation{}, fmt.Errorf("generation must be non-negative: %d", value)
	}
	return Generation{value: uint64(value)}, nil
}

func (generation Generation) Value() uint64 {
	return generation.value
}

// InstanceUID is the opaque, server-assigned identity of a Scenario Instance.
type InstanceUID struct {
	value string
}

// ParseInstanceUID validates a bounded printable UID without assuming a UUID
// implementation.
func ParseInstanceUID(value string) (InstanceUID, error) {
	if !instanceUIDPattern.MatchString(value) {
		return InstanceUID{}, fmt.Errorf("%q is not a valid opaque instance UID", value)
	}
	return InstanceUID{value: value}, nil
}

func (uid InstanceUID) String() string {
	return uid.value
}

// SyntheticNodeName derives a stable DNS label from exact instance identity,
// group, and replica inputs without exposing a backend name.
func SyntheticNodeName(
	instanceName Name,
	instanceUID InstanceUID,
	group Name,
	replica uint64,
) (Name, error) {
	if instanceName.value == "" || instanceUID.value == "" || group.value == "" {
		return Name{}, fmt.Errorf("Synthetic Node identity requires instance, UID, and group")
	}
	digest := deterministicIdentity(
		"kasim.synthetic-node.v1",
		instanceName.value,
		instanceUID.value,
		group.value,
		strconv.FormatUint(replica, 10),
	)
	return ParseName("kasim-node-" + digest[:24])
}

// SimulatedDeviceID derives a stable simulator-owned identity. It is never a
// claim about a vendor hardware identifier.
func SimulatedDeviceID(
	instanceUID InstanceUID,
	group Name,
	replica uint64,
	pool Name,
	deviceIndex uint64,
) (string, error) {
	if instanceUID.value == "" || group.value == "" || pool.value == "" {
		return "", fmt.Errorf("simulated device identity requires UID, group, and pool")
	}
	digest := deterministicIdentity(
		"kasim.simulated-device.v1",
		instanceUID.value,
		group.value,
		strconv.FormatUint(replica, 10),
		pool.value,
		strconv.FormatUint(deviceIndex, 10),
	)
	return "kasim-device-" + digest[:32], nil
}

func deterministicIdentity(domain string, values ...string) string {
	digester := sha256.New()
	digester.Write([]byte(domain))
	for _, value := range values {
		digester.Write([]byte{0})
		digester.Write([]byte(value))
	}
	return hex.EncodeToString(digester.Sum(nil))
}
