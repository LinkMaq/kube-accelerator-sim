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

	const expected = `catalog 2026-07-30 sha256:571e5b3d6a59e5aa641cbb61c6719914140c5e031b38ff08feacb0642a72c260
amd 2026-07-30 sha256:a360dbb213a910fcee34eea960ad4d9021a9f2f801becb967c14fea76d2b2fca
aws-neuron 2026-07-30 sha256:91d50953e6d1dc76ddd602b08a215938062cdad7fa2b4414e8b2766447575756
biren 2026-07-30 sha256:2a069bb0b61a92b47cce38d9213c091c0c76e92136cc097ee2e14aff0520fe4a
cambricon 2026-07-30 sha256:8c45850ddf194f5752b240ca05b6f71c1c5e7dde406f6b32ce841a4ea2799417
enflame 2026-07-30 sha256:12d285901e12e6b3494454cbb4adcfcd58df1eea3f4dd6546cdaf27712459d82
furiosa 2026-07-30 sha256:f05d79dfed7c38d29ac1f1b31931d200299f8c3712856acf0a0d52676df44d1e
google-tpu 2026-07-30 sha256:145994176db738324df8a4bcf573ff109dc72df60e586f30676dc1cca89ac83d
graphcore 2026-07-30 sha256:056fea94141b281c7d553a2670ca6e52faa1d5938b954b3715c709fe3927f4d3
huawei-ascend 2026-07-30 sha256:e94ee314cf9b9076350e79666a7f660283b0d83c9b40de1c33c6707012737519
hygon 2026-07-30 sha256:19c324d2d7467b142416351e3d7f59fa2de5fedc83de0b910f13541cdf7e543a
iluvatar 2026-07-30 sha256:f19beef220644dc7bc093f75b91a387867cdaa727e99b5c577d515e9252dbaeb
intel-gaudi 2026-07-30 sha256:0a18136f17f936d0036842a7c16051492b894a31c1215246581a9f1c149a06e5
intel-gpu 2026-07-30 sha256:b7f8a6a57cbf8b21451fb6a34694da00cf9203d3ed2ff4ab3e29cb19cc08c64c
kunlunxin 2026-07-30 sha256:2fdaaf618cf9498cb50fb7fbf95ac9d178fca35948541d06d7c69140aa38618a
metax 2026-07-30 sha256:3c07c5e3415d6d96bc28bd6dff26d8f287e8235ff58efeb42b65db6d4f2c76c8
moore-threads 2026-07-30 sha256:b10ae6ea18bc332ceb24316dc40c58db87374981458acbf0f15403d38ce786af
nvidia 2026-07-30 sha256:dfd6878266ba81287632d5a0cc9d5fe8856d2839ac735a460929ba5d7f519705
qualcomm-cloud-ai-100 2026-07-30 sha256:9af51c8a8b0d6a79a7c94782471e57c370683742d627121375dfb6a74814df5d
vastai 2026-07-30 sha256:c35cc4387fab60f7a2fdd1dc24502a5b3e52bb8026ebfb66e9a7fec58d1853c7
`
	if actual.String() != expected {
		t.Fatalf("catalog digest golden changed:\n%s", actual.String())
	}
}
