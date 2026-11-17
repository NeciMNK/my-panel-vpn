package runtime

import (
	"github.com/mhsanaei/3x-ui/v3/internal/amneziawg"
	"github.com/mhsanaei/3x-ui/v3/internal/amneziawgkern"
	"github.com/mhsanaei/3x-ui/v3/internal/amneziawgnet"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/mtproto"
	"github.com/mhsanaei/3x-ui/v3/internal/tuic"
)

// localEngine serves one protocol that runs outside xray-core on this host.
type localEngine interface {
	add(ib *model.Inbound) error
	// update reconfigures in place; a Del+Add would drop the manager state that keeps live sessions.
	update(ib *model.Inbound) error
	remove(id int)
	// usersChanged follows a per-user add/remove; clients otherwise ride on update.
	usersChanged()
}

// newLocalEngines must hold an engine for every protocol.Spec not served by Xray.
func newLocalEngines(deps LocalDeps) map[model.Protocol]localEngine {
	return map[model.Protocol]localEngine{
		model.MTProto:   mtprotoEngine{},
		model.AmneziaWG: amneziaWGEngine{deps: deps},
		model.TUIC:      tuicEngine{deps: deps},
	}
}

type mtprotoEngine struct{}

func (mtprotoEngine) add(ib *model.Inbound) error {
	inst, ok := mtproto.InstanceFromInbound(ib)
	if !ok {
		return nil
	}
	return mtproto.GetManager().Ensure(inst)
}

func (mtprotoEngine) update(ib *model.Inbound) error {
	inst, ok := mtproto.InstanceFromInbound(ib)
	if !ib.Enable || !ok {
		mtproto.GetManager().Remove(ib.Id)
		return nil
	}
	return mtproto.GetManager().Ensure(inst)
}

func (mtprotoEngine) remove(id int) { mtproto.GetManager().Remove(id) }

func (mtprotoEngine) usersChanged() {}

// amneziaWGEngine flags an Xray resync on every change: injectAmneziawgnetSocks
// keeps an Xray-side relay inbound per AmneziaWG inbound that must follow it.
type amneziaWGEngine struct{ deps LocalDeps }

func (e amneziaWGEngine) needXrayResync() {
	if e.deps.SetNeedRestart != nil {
		e.deps.SetNeedRestart()
	}
}

func (e amneziaWGEngine) add(ib *model.Inbound) error {
	inst, ok := amneziawg.InstanceFromInbound(ib)
	if !ok {
		return nil
	}
	err := ensureAmneziaWG(inst)
	e.needXrayResync()
	return err
}

func (e amneziaWGEngine) update(ib *model.Inbound) error {
	e.needXrayResync()
	inst, ok := amneziawg.InstanceFromInbound(ib)
	if !ib.Enable || !ok {
		removeAmneziaWG(ib.Id)
		return nil
	}
	return ensureAmneziaWG(inst)
}

func (e amneziaWGEngine) remove(id int) {
	removeAmneziaWG(id)
	e.needXrayResync()
}

func (amneziaWGEngine) usersChanged() {}

// ensureAmneziaWG routes an inbound to its own datapath's manager and clears
// the other's: both bind the inbound's UDP port, so they must never overlap.
func ensureAmneziaWG(inst amneziawg.Instance) error {
	if inst.KernelMode {
		amneziawgnet.GetManager().Remove(inst.Id)
		return amneziawgkern.GetManager().Ensure(amneziawgkern.Desired{
			Instance:         inst,
			EgressInterface:  inst.ExternalInterface,
			RouteThroughXray: inst.RouteThroughXray,
		})
	}
	if err := amneziawgkern.GetManager().Remove(inst.Id); err != nil {
		return err
	}
	return amneziawgnet.GetManager().Ensure(amneziawgnet.Desired{
		Instance: inst,
		Options: amneziawgnet.DeviceOptions{
			HeaderProtectionKey:    inst.Obfuscation.HeaderProtectionKey,
			ContentPaddingAddition: inst.Obfuscation.ContentPaddingAddition,
			RekeyAfterTime:         inst.Obfuscation.RekeyAfterTime,
			RekeyTimeout:           inst.Obfuscation.RekeyTimeout,
			RejectAfterTime:        inst.Obfuscation.RejectAfterTime,
			KeepaliveTimeout:       inst.Obfuscation.KeepaliveTimeout,
			MaxHandshakeAttempts:   inst.Obfuscation.MaxHandshakeAttempts,
			RandomTrailers:         inst.Obfuscation.RandomTrailers,
			DisableCookies:         inst.Obfuscation.DisableCookies,
		},
	})
}

// removeAmneziaWG tears the inbound down on whichever datapath is holding it.
func removeAmneziaWG(id int) {
	amneziawgnet.GetManager().Remove(id)
	if err := amneziawgkern.GetManager().Remove(id); err != nil {
		logger.Warning("amneziawgkern: removing inbound ", id, ": ", err)
	}
}

// tuicEngine asks for a reconcile on every change: each TUIC inbound has an
// Xray SOCKS relay whose accounts and tag must follow it after commit.
type tuicEngine struct{ deps LocalDeps }

func (e tuicEngine) reconcileRelay() {
	if e.deps.RequestReconcile != nil {
		e.deps.RequestReconcile()
	}
}

func (e tuicEngine) add(ib *model.Inbound) error {
	inst, ok := tuic.InstanceFromInbound(ib)
	if !ok {
		return nil
	}
	err := tuic.GetManager().Ensure(inst)
	e.reconcileRelay()
	return err
}

func (e tuicEngine) update(ib *model.Inbound) error {
	e.reconcileRelay()
	inst, ok := tuic.InstanceFromInbound(ib)
	if !ib.Enable || !ok {
		tuic.GetManager().Remove(ib.Id)
		return nil
	}
	return tuic.GetManager().Ensure(inst)
}

func (e tuicEngine) remove(id int) {
	tuic.GetManager().Remove(id)
	e.reconcileRelay()
}

func (e tuicEngine) usersChanged() { e.reconcileRelay() }
