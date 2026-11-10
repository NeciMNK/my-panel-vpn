package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

// ProxyNodeXray relays an Xray-settings request to a node so the master's Xray
// page can manage that node's outbounds, routing, balancers and WARP.
func (s *NodeService) ProxyNodeXray(ctx context.Context, nodeID int, method, subPath, rawQuery, contentType string, body []byte) (*runtime.XrayProxyResponse, error) {
	mgr := runtime.GetManager()
	if mgr == nil {
		return nil, errors.New("node runtime is not initialised")
	}
	rt, err := mgr.RuntimeFor(&nodeID)
	if err != nil {
		return nil, err
	}
	remote, ok := rt.(*runtime.Remote)
	if !ok {
		return nil, fmt.Errorf("node %d has no remote runtime", nodeID)
	}
	return remote.ProxyXray(ctx, method, subPath, rawQuery, contentType, body)
}
