package catalog_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
)

func TestBundledCatalogDigestGolden(t *testing.T) {
	t.Parallel()

	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	var actual strings.Builder
	fmt.Fprintf(
		&actual,
		"catalog %s %s\n",
		snapshot.Revision(),
		snapshot.Digest(),
	)
	for _, profile := range snapshot.List() {
		fmt.Fprintf(
			&actual,
			"%s %s %s\n",
			profile.ID(),
			profile.Revision(),
			profile.Digest(),
		)
	}

	const expected = `catalog 2026-08-03 sha256:549363248f2c21131fdabe38fef424ff34ec13be0774a89fe70f07f7c0d737f5
amd 2026-08-03 sha256:c165c4b0616a2f4064e6a1805e14d97ed76b66d632a9c050fda89cd8b15d2bea
aws-neuron 2026-08-03 sha256:91d50953e6d1dc76ddd602b08a215938062cdad7fa2b4414e8b2766447575756
biren 2026-08-03 sha256:2a069bb0b61a92b47cce38d9213c091c0c76e92136cc097ee2e14aff0520fe4a
cambricon 2026-08-03 sha256:3c68ef76743e2cee037d4072f6be5d67b361138e6e00cd658d9e024507c93edf
enflame 2026-08-03 sha256:12d285901e12e6b3494454cbb4adcfcd58df1eea3f4dd6546cdaf27712459d82
furiosa 2026-08-03 sha256:f05d79dfed7c38d29ac1f1b31931d200299f8c3712856acf0a0d52676df44d1e
google-tpu 2026-08-03 sha256:145994176db738324df8a4bcf573ff109dc72df60e586f30676dc1cca89ac83d
graphcore 2026-08-03 sha256:056fea94141b281c7d553a2670ca6e52faa1d5938b954b3715c709fe3927f4d3
huawei-ascend 2026-08-03 sha256:d4fbba0cf3ddb9b57f98d3cb584010d601baecaa9b0500a108df32e94642629d
hygon 2026-08-03 sha256:f9c9a7fc5f06b32dd18759cd6634b53df7ec83e6d0231ede35f39be61cebe242
iluvatar 2026-08-03 sha256:f19beef220644dc7bc093f75b91a387867cdaa727e99b5c577d515e9252dbaeb
intel-gaudi 2026-08-03 sha256:0a18136f17f936d0036842a7c16051492b894a31c1215246581a9f1c149a06e5
intel-gpu 2026-08-03 sha256:b7f8a6a57cbf8b21451fb6a34694da00cf9203d3ed2ff4ab3e29cb19cc08c64c
kunlunxin-hami 2026-08-03 sha256:5c5b606b7b3b84e37a816201869fbb24e558f15b28ff1a9b291772153cfe5e10
metax 2026-08-03 sha256:c39da7311ee12247f621832c3a4f24bcbfa8b6563bd2eaf1014615d25a959ebd
moore-threads 2026-08-03 sha256:fa8bb573c8e6c7eeb89d8ae8503d97b2b389598674d31e69ec79117519f08a30
nvidia 2026-08-03 sha256:15fa27b98c21e0b3bc60661acd0b4835c7e16e5c8b5c949334048ca08f3731de
qualcomm-cloud-ai-100 2026-08-03 sha256:9af51c8a8b0d6a79a7c94782471e57c370683742d627121375dfb6a74814df5d
rdma-shared-device-plugin 2026-08-03 sha256:47a91b9586b98c5362610722f0b2fe3ee278d773717d6748d88e890d6974a6d8
sriov-network-device-plugin 2026-08-03 sha256:3e925812d245b51146c0bcc833b276244c93aa76412c29ab3a4735bca9eb44cd
vastai-hami 2026-08-03 sha256:d33467956d1d80197b689f5616ecbc1d3a9ed135b0a44cdb577a5cb8a359456c
`
	if actual.String() != expected {
		t.Fatalf("catalog digest golden changed:\n%s", actual.String())
	}
}
