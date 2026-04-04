package store

import (
	"strings"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

func summarizeNodeRuntimeTunnels(items []types.TunnelSpec) types.NodeRuntimeSummary {
	summary := types.NodeRuntimeSummary{}
	for _, tunnel := range items {
		if tunnel.Status != "active" {
			continue
		}
		summary.ActiveTunnelCount++

		runtimePath := strings.TrimSpace(tunnel.RuntimePath)
		if runtimePath == "" && tunnel.Metadata != nil {
			runtimePath = strings.TrimSpace(tunnel.Metadata["runtimePath"])
		}
		switch runtimePath {
		case types.TunnelRuntimePathRelay:
			summary.RelayPathCount++
		case types.TunnelRuntimePathP2P:
			summary.P2PPathCount++
		}

		runtimeState := strings.TrimSpace(tunnel.RuntimeState)
		if runtimeState == "" && tunnel.Metadata != nil {
			runtimeState = strings.TrimSpace(tunnel.Metadata["runtimeState"])
		}
		switch runtimeState {
		case types.TunnelRuntimeStatePending:
			summary.PendingStateCount++
		case types.TunnelRuntimeStateUnavailable:
			summary.UnavailableStateCount++
		}

		failureReason := strings.TrimSpace(tunnel.LastFailureReason)
		if failureReason == "" && tunnel.Metadata != nil {
			failureReason = strings.TrimSpace(tunnel.Metadata["lastFailureReason"])
		}
		if failureReason != "" {
			summary.FailureReasonCount++
		}
	}
	return summary
}
