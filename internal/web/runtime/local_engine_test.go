package runtime

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/protocol"
)

// A protocol served outside Xray but missing an engine would be sent to the Xray
// API as an inbound xray-core cannot build; an engine on an Xray protocol hides it from Xray.
func TestLocalEnginesCoverExactlyTheProtocolsServedOutsideXray(t *testing.T) {
	local := NewLocal(LocalDeps{})
	for _, spec := range protocol.All() {
		_, hasEngine := local.engines[spec.ID]
		if hasEngine == spec.ServedByXray() {
			t.Errorf("%s: served by Xray = %t, has a local engine = %t", spec.ID, spec.ServedByXray(), hasEngine)
		}
	}
}
