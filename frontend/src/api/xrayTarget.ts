import { useSyncExternalStore } from 'react';

// Node whose Xray template the Xray page edits; null is this panel's own core.
// Module state because plain fetchers (not only hooks) build xray URLs.
let targetNodeId: number | null = null;
const listeners = new Set<() => void>();

export function getXrayTargetNode(): number | null {
  return targetNodeId;
}

export function setXrayTargetNode(nodeId: number | null): void {
  if (nodeId === targetNodeId) return;
  targetNodeId = nodeId;
  listeners.forEach((l) => l());
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function useXrayTargetNode(): number | null {
  return useSyncExternalStore(subscribe, getXrayTargetNode, getXrayTargetNode);
}

// xrayApi('/update') → '/panel/api/xray/update' locally, or the master's proxy
// '/panel/api/nodes/<id>/xray/update' while a node is selected.
export function xrayApi(path: string): string {
  const sub = path.startsWith('/') ? path : `/${path}`;
  if (targetNodeId === null) return `/panel/api/xray${sub}`;
  return `/panel/api/nodes/${targetNodeId}/xray${sub}`;
}
