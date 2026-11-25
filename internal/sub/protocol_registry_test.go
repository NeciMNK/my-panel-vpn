package sub

import (
	"fmt"
	"slices"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/protocol"
)

// A protocol carrying clients but missing from the subscription query silently
// drops its inbounds from every subscriber's links; one without clients must not appear.
func TestLoadInboundsBySubIdListsExactlyTheProtocolsWithClients(t *testing.T) {
	initSubDB(t)
	db := database.GetDB()
	client := model.ClientRecord{Email: "registry@sub", SubID: "registry-sub", Enable: true}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	for i, spec := range protocol.All() {
		ib := model.Inbound{
			Tag: fmt.Sprintf("registry-%s", spec.ID), Port: 20000 + i, Protocol: spec.ID,
			Enable: true, Settings: `{"clients":[]}`,
		}
		if err := db.Create(&ib).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ClientInbound{ClientId: client.Id, InboundId: ib.Id}).Error; err != nil {
			t.Fatal(err)
		}
	}

	inbounds, err := (&SubService{}).loadInboundsBySubId("registry-sub")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ib := range inbounds {
		got = append(got, string(ib.Protocol))
	}
	want := protocol.WithClients()
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("subscription inbounds by protocol %v, want %v", got, want)
	}
}

// A ShareLink protocol without a generator yields "" for every client, so its
// subscribers get no link and no QR; a generator on any other protocol is dead code.
func TestShareLinkGeneratorsCoverExactlyTheShareLinkProtocols(t *testing.T) {
	for _, spec := range protocol.All() {
		_, has := shareLinkGenerators[spec.ID]
		if has != spec.ShareLink {
			t.Errorf("%s: ShareLink = %t, has a link generator = %t", spec.ID, spec.ShareLink, has)
		}
	}
}
