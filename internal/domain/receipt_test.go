package domain_test

import (
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

func TestRevisionReceiptCarriesTargetIdentityAndImmutableDigests(t *testing.T) {
	t.Parallel()

	instanceName, err := domain.ParseName("training-lab")
	if err != nil {
		t.Fatal(err)
	}
	uid, err := domain.ParseInstanceUID("instance-uid")
	if err != nil {
		t.Fatal(err)
	}
	target, err := domain.ParseDigest(
		"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := domain.ParseDigest(
		"sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := domain.ParseDigest(
		"sha256:1111111111111111111111111111111111111111111111111111111111111111",
	)
	if err != nil {
		t.Fatal(err)
	}
	desired, err := domain.NewGeneration(2)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := domain.NewGeneration(1)
	if err != nil {
		t.Fatal(err)
	}

	receipt, err := domain.NewRevisionReceipt(domain.RevisionReceiptInput{
		ContextName:        "development",
		TargetFingerprint:  target,
		InstanceName:       instanceName,
		InstanceUID:        uid,
		DesiredGeneration:  desired,
		ObservedGeneration: observed,
		RevisionDigest:     revision,
		ProfileDigests:     []domain.Digest{profile},
		RevisionAccepted:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if receipt.ContextName() != "development" ||
		receipt.TargetFingerprint() != target ||
		receipt.InstanceName() != instanceName ||
		receipt.InstanceUID() != uid ||
		receipt.DesiredGeneration() != desired ||
		receipt.ObservedGeneration() != observed ||
		receipt.RevisionDigest() != revision ||
		receipt.NoOp() ||
		!receipt.RevisionAccepted() {
		t.Fatalf("receipt identity was not preserved: %#v", receipt)
	}
	returned := receipt.ProfileDigests()
	returned[0] = target
	if receipt.ProfileDigests()[0] != profile {
		t.Fatal("receipt exposed mutable profile digest storage")
	}
}
