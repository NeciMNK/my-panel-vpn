package service

import (
	"errors"
	"slices"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// A regular user ended up on a hidden cascade-link inbound: its exit then carried
// the user directly, outside the cascade's routing and the panel's own pins.
func TestRegularClientsStayOffManagedInbounds(t *testing.T) {
	setupConflictDB(t)
	seedInboundConflict(t, "mp-cascade-1-in-n0", "0.0.0.0", 8443, model.VLESS, `{"network":"tcp"}`, `{"clients":[]}`)
	seedInboundConflict(t, "vless-1", "0.0.0.0", 8444, model.VLESS, `{"network":"tcp"}`, `{"clients":[]}`)
	var managed, regular model.Inbound
	if err := database.GetDB().Where("tag = ?", "mp-cascade-1-in-n0").First(&managed).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Where("tag = ?", "vless-1").First(&regular).Error; err != nil {
		t.Fatal(err)
	}
	s, inboundSvc := &ClientService{}, &InboundService{}

	_, err := s.Create(inboundSvc, &ClientCreatePayload{
		Client:     model.Client{Email: "u1", Enable: true},
		InboundIds: []int{managed.Id},
	})
	if !errors.Is(err, errManagedInboundClient) {
		t.Fatalf("create u1 on the link inbound: err = %v, want %v", err, errManagedInboundClient)
	}
	if _, err := s.Create(inboundSvc, &ClientCreatePayload{
		Client:     model.Client{Email: "u2", Enable: true},
		InboundIds: []int{regular.Id},
	}); err != nil {
		t.Fatalf("create u2 on a regular inbound: %v", err)
	}
	if _, err := s.AttachByEmail(inboundSvc, "u2", []int{managed.Id}); !errors.Is(err, errManagedInboundClient) {
		t.Fatalf("attach u2 to the link inbound: err = %v, want %v", err, errManagedInboundClient)
	}
	if _, err := s.Create(inboundSvc, &ClientCreatePayload{
		Client:     model.Client{Email: "mp-cascade-1-n0", Enable: true},
		InboundIds: []int{managed.Id},
	}); err != nil {
		t.Fatalf("the link's own client was refused: %v", err)
	}
}

// The client and inbound pickers list these options, which is how u1 got picked.
func TestInboundOptionsLeaveOutManagedInbounds(t *testing.T) {
	setupConflictDB(t)
	seedInboundConflict(t, "mp-home-2-in-n0", "0.0.0.0", 8443, model.VLESS, `{"network":"tcp"}`, `{"clients":[]}`)
	seedInboundConflict(t, "vless-1", "0.0.0.0", 8444, model.VLESS, `{"network":"tcp"}`, `{"clients":[]}`)
	var userID int
	if err := database.GetDB().Model(&model.Inbound{}).Where("tag = ?", "vless-1").Pluck("user_id", &userID).Error; err != nil {
		t.Fatal(err)
	}
	opts, err := (&InboundService{}).GetInboundOptions(userID)
	if err != nil {
		t.Fatalf("GetInboundOptions: %v", err)
	}
	var tags []string
	for _, o := range opts {
		tags = append(tags, o.Tag)
	}
	if !slices.Equal(tags, []string{"vless-1"}) {
		t.Fatalf("option tags = %v, want [vless-1]", tags)
	}
}
