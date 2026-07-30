package domain

import "fmt"

// RevisionReceiptInput contains the identity evidence required for one
// target-connected lifecycle result.
type RevisionReceiptInput struct {
	ContextName        string
	TargetFingerprint  Digest
	InstanceName       Name
	InstanceUID        InstanceUID
	DesiredGeneration  Generation
	ObservedGeneration Generation
	RevisionDigest     Digest
	ProfileDigests     []Digest
	RevisionAccepted   bool
	NoOp               bool
}

// RevisionReceipt is immutable evidence of a proposed, accepted, or no-op
// revision against an explicit Simulation Target.
type RevisionReceipt struct {
	contextName        string
	targetFingerprint  Digest
	instanceName       Name
	instanceUID        InstanceUID
	desiredGeneration  Generation
	observedGeneration Generation
	revisionDigest     Digest
	profileDigests     []Digest
	revisionAccepted   bool
	noOp               bool
}

// NewRevisionReceipt validates required target identity and copies all digest
// collections.
func NewRevisionReceipt(input RevisionReceiptInput) (RevisionReceipt, error) {
	if len(input.ContextName) == 0 || len(input.ContextName) > 253 {
		return RevisionReceipt{}, fmt.Errorf("revision receipt requires a bounded context name")
	}
	for _, character := range input.ContextName {
		if character < ' ' || character > '~' {
			return RevisionReceipt{}, fmt.Errorf("context name contains unsupported characters")
		}
	}
	if input.TargetFingerprint.value == "" {
		return RevisionReceipt{}, fmt.Errorf("revision receipt requires a target fingerprint")
	}
	if input.InstanceName.value == "" {
		return RevisionReceipt{}, fmt.Errorf("revision receipt requires an instance name")
	}
	if input.RevisionAccepted && input.InstanceUID.value == "" {
		return RevisionReceipt{}, fmt.Errorf("accepted revision receipt requires an instance UID")
	}
	if input.ObservedGeneration.value > input.DesiredGeneration.value {
		return RevisionReceipt{}, fmt.Errorf("observed generation cannot exceed desired generation")
	}
	if input.RevisionDigest.value == "" {
		return RevisionReceipt{}, fmt.Errorf("revision receipt requires a revision digest")
	}
	for _, digest := range input.ProfileDigests {
		if digest.value == "" {
			return RevisionReceipt{}, fmt.Errorf("revision receipt contains an invalid profile digest")
		}
	}
	if input.NoOp && input.RevisionAccepted {
		return RevisionReceipt{}, fmt.Errorf("no-op receipt cannot report a newly accepted revision")
	}
	return RevisionReceipt{
		contextName:        input.ContextName,
		targetFingerprint:  input.TargetFingerprint,
		instanceName:       input.InstanceName,
		instanceUID:        input.InstanceUID,
		desiredGeneration:  input.DesiredGeneration,
		observedGeneration: input.ObservedGeneration,
		revisionDigest:     input.RevisionDigest,
		profileDigests:     append([]Digest(nil), input.ProfileDigests...),
		revisionAccepted:   input.RevisionAccepted,
		noOp:               input.NoOp,
	}, nil
}

func (receipt RevisionReceipt) ContextName() string {
	return receipt.contextName
}

func (receipt RevisionReceipt) TargetFingerprint() Digest {
	return receipt.targetFingerprint
}

func (receipt RevisionReceipt) InstanceName() Name {
	return receipt.instanceName
}

func (receipt RevisionReceipt) InstanceUID() InstanceUID {
	return receipt.instanceUID
}

func (receipt RevisionReceipt) DesiredGeneration() Generation {
	return receipt.desiredGeneration
}

func (receipt RevisionReceipt) ObservedGeneration() Generation {
	return receipt.observedGeneration
}

func (receipt RevisionReceipt) RevisionDigest() Digest {
	return receipt.revisionDigest
}

func (receipt RevisionReceipt) ProfileDigests() []Digest {
	return append([]Digest(nil), receipt.profileDigests...)
}

func (receipt RevisionReceipt) RevisionAccepted() bool {
	return receipt.revisionAccepted
}

func (receipt RevisionReceipt) NoOp() bool {
	return receipt.noOp
}
