package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

type controlPreflightBuilder struct {
	summary types.ControlPreflightSummary
}

func newControlPreflightBuilder() *controlPreflightBuilder {
	return &controlPreflightBuilder{
		summary: types.ControlPreflightSummary{
			Allowed:        true,
			Items:          []types.ControlCheckItem{},
			BlockedReasons: []types.ControlBlockedReason{},
		},
	}
}

func (b *controlPreflightBuilder) addCheck(code, label string, state types.ControlCheckState, message string, reasonCode types.ControlReasonCode) {
	b.summary.Items = append(b.summary.Items, types.ControlCheckItem{
		Code:    code,
		Label:   label,
		State:   state,
		Message: message,
	})
	if state == types.ControlCheckBlocked {
		b.block(reasonCode, message)
	}
}

func (b *controlPreflightBuilder) block(reasonCode types.ControlReasonCode, message string) {
	b.summary.Allowed = false
	b.summary.BlockedReasons = append(b.summary.BlockedReasons, types.ControlBlockedReason{
		Code:    reasonCode,
		Message: message,
	})
}

func (b *controlPreflightBuilder) summaryValue() types.ControlPreflightSummary {
	return b.summary
}

func (s *Server) handleControlActions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	req, err := decodeControlActionRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, status := s.evaluateControlAction(r.Context(), req)
	writeJSON(w, status, resp)
}

func decodeControlActionRequest(r *http.Request) (types.ControlActionRequest, error) {
	var req types.ControlActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return req, errInvalidControlPayload
	}
	if strings.TrimSpace(req.TargetID) == "" {
		return req, errMissingControlTargetID
	}
	if req.RequestedAt.IsZero() {
		req.RequestedAt = time.Now().UTC()
	}
	return req, nil
}

func (s *Server) evaluateControlAction(ctx context.Context, req types.ControlActionRequest) (types.ControlActionResponse, int) {
	resp := newControlActionResponse(req)
	preflight, status := s.evaluateControlPreflight(ctx, req, &resp)
	resp.Preflight = preflight
	if status != http.StatusOK {
		return resp, status
	}
	if req.DryRun {
		resp.Result = ternaryControlResult(preflight.Allowed, types.ControlResultAccepted, types.ControlResultBlocked)
		resp.HumanMessage = ternaryString(preflight.Allowed, "preflight 已完成，当前只执行 dry-run。", "preflight 已阻断，当前不允许进入 execute。")
		resp.DryRunOnly = true
		resp.ExecutionMode = types.ControlExecutionPlaceholder
		return resp, http.StatusOK
	}
	return s.executePlaceholderControlAction(resp), http.StatusOK
}

func newControlActionResponse(req types.ControlActionRequest) types.ControlActionResponse {
	return types.ControlActionResponse{
		ActionKind:    req.ActionKind,
		TargetKind:    req.TargetKind,
		TargetID:      req.TargetID,
		SourceSurface: req.SourceSurface,
		DryRunOnly:    req.DryRun,
		ExecutionMode: types.ControlExecutionPlaceholder,
		Facts:         map[string]string{},
	}
}

func (s *Server) evaluateControlPreflight(ctx context.Context, req types.ControlActionRequest, resp *types.ControlActionResponse) (types.ControlPreflightSummary, int) {
	builder := newControlPreflightBuilder()
	switch req.TargetKind {
	case types.ControlTargetNode:
		status := s.evaluateNodeControlPreflight(ctx, req, resp, builder)
		return builder.summaryValue(), status
	case types.ControlTargetTunnel:
		status := s.evaluateTunnelControlPreflight(ctx, req, resp, builder)
		return builder.summaryValue(), status
	default:
		resp.Result = types.ControlResultRejected
		resp.HumanMessage = "当前 targetKind 不受支持。"
		builder.block(types.ControlReasonUnsupportedAction, "当前 targetKind 不受支持。")
		return builder.summaryValue(), http.StatusBadRequest
	}
}

func (s *Server) evaluateNodeControlPreflight(ctx context.Context, req types.ControlActionRequest, resp *types.ControlActionResponse, builder *controlPreflightBuilder) int {
	node, err := s.store.GetNode(ctx, req.TargetID)
	if err != nil {
		resp.Result = types.ControlResultBlocked
		resp.HumanMessage = "未找到目标节点。"
		builder.block(types.ControlReasonTargetNotFound, "未找到目标节点。")
		return http.StatusNotFound
	}
	populateNodeControlFacts(resp, node)
	addNodeSurfaceCheck(req, builder)
	builder.addCheck("online", "节点 online", requiredCheck(node.Status == "online"), ternaryMessage(node.Status == "online", "当前节点在线。", "当前节点离线。"), types.ControlReasonNodeOffline)
	builder.addCheck("managed", "受管实例", requiredCheck(node.InstanceManaged), ternaryMessage(node.InstanceManaged, "当前节点为受管实例。", "当前节点不是受管实例。"), types.ControlReasonUnmanagedInstance)
	builder.addCheck("deployment", "deploymentMode", requiredCheck(strings.TrimSpace(node.DeploymentMode) != ""), ternaryMessage(strings.TrimSpace(node.DeploymentMode) != "", node.DeploymentMode, "当前还没有上报 deploymentMode。"), types.ControlReasonMissingDeploymentMode)
	builder.addCheck("serviceUnit", "serviceUnit 已上报", requiredCheck(strings.TrimSpace(node.ServiceUnit) != ""), ternaryMessage(strings.TrimSpace(node.ServiceUnit) != "", node.ServiceUnit, "当前还没有上报 serviceUnit。"), types.ControlReasonMissingServiceUnit)
	builder.addCheck("instanceProfile", "instanceProfile 已上报", requiredCheck(strings.TrimSpace(node.InstanceProfile) != ""), ternaryMessage(strings.TrimSpace(node.InstanceProfile) != "", node.InstanceProfile, "当前还没有上报 instanceProfile。"), types.ControlReasonMissingInstanceProfile)

	switch req.ActionKind {
	case types.ControlActionRestartAgent:
		if node.Isolated {
			builder.addCheck("isolation", "节点未隔离", types.ControlCheckBlocked, "当前节点已隔离，未来 restart_agent 仍应阻断。", types.ControlReasonNodeIsolated)
		} else {
			builder.addCheck("isolation", "节点未隔离", types.ControlCheckPass, "当前节点未隔离。", "")
		}
	case types.ControlActionIsolateNode:
		if req.SourceSurface != types.ControlSurfaceOperatorConsole {
			builder.addCheck("surfaceAction", "动作适用界面", types.ControlCheckBlocked, "isolate_node 目前只允许 operator-console 发起。", types.ControlReasonUnsupportedSurface)
		}
		if node.Isolated {
			builder.addCheck("isolation", "节点未隔离", types.ControlCheckBlocked, "当前节点已经隔离，不能重复 isolate。", types.ControlReasonNodeIsolated)
		} else {
			builder.addCheck("isolation", "节点未隔离", types.ControlCheckPass, "当前节点未隔离。", "")
		}
	case types.ControlActionReleaseNode:
		if req.SourceSurface != types.ControlSurfaceOperatorConsole {
			builder.addCheck("surfaceAction", "动作适用界面", types.ControlCheckBlocked, "release_node 目前只允许 operator-console 发起。", types.ControlReasonUnsupportedSurface)
		}
		if !node.Isolated {
			builder.addCheck("isolation", "节点已隔离", types.ControlCheckBlocked, "当前节点未隔离，不能执行 release。", types.ControlReasonNodeNotIsolated)
		} else {
			builder.addCheck("isolation", "节点已隔离", types.ControlCheckPass, "当前节点已隔离。", "")
		}
	default:
		builder.addCheck("action", "动作白名单", types.ControlCheckBlocked, "当前节点动作不在本轮白名单内。", types.ControlReasonUnsupportedAction)
	}
	return http.StatusOK
}

func (s *Server) evaluateTunnelControlPreflight(ctx context.Context, req types.ControlActionRequest, resp *types.ControlActionResponse, builder *controlPreflightBuilder) int {
	tunnel, err := s.store.GetTunnel(ctx, req.TargetID)
	if err != nil {
		resp.Result = types.ControlResultBlocked
		resp.HumanMessage = "未找到目标 tunnel。"
		builder.block(types.ControlReasonTargetNotFound, "未找到目标 tunnel。")
		return http.StatusNotFound
	}
	populateTunnelControlFacts(resp, tunnel)
	switch req.SourceSurface {
	case types.ControlSurfaceNodeConsole, types.ControlSurfaceOperatorConsole:
		builder.addCheck("surface", "来源界面", types.ControlCheckPass, "当前来源界面已被允许。", "")
	default:
		builder.addCheck("surface", "来源界面", types.ControlCheckBlocked, "当前来源界面不受支持。", types.ControlReasonUnsupportedSurface)
	}

	switch req.ActionKind {
	case types.ControlActionPauseTunnel:
		if tunnel.Status == "paused" {
			builder.addCheck("statusConflict", "tunnel 可暂停", types.ControlCheckBlocked, "当前 tunnel 已经是 paused。", types.ControlReasonTunnelStateConflict)
		} else {
			builder.addCheck("statusConflict", "tunnel 可暂停", types.ControlCheckPass, "当前 tunnel 允许进入 paused。", "")
		}
	case types.ControlActionResumeTunnel:
		if tunnel.Status != "paused" {
			builder.addCheck("statusConflict", "tunnel 可恢复", types.ControlCheckBlocked, "当前 tunnel 不是 paused，不能 resume。", types.ControlReasonTunnelStateConflict)
		} else {
			builder.addCheck("statusConflict", "tunnel 可恢复", types.ControlCheckPass, "当前 tunnel 允许恢复 active。", "")
		}
	default:
		builder.addCheck("action", "动作白名单", types.ControlCheckBlocked, "当前 tunnel 动作不在本轮白名单内。", types.ControlReasonUnsupportedAction)
	}
	return http.StatusOK
}

func (s *Server) executePlaceholderControlAction(resp types.ControlActionResponse) types.ControlActionResponse {
	if !resp.Preflight.Allowed {
		resp.Result = types.ControlResultBlocked
		resp.HumanMessage = "execute 已被预检阻断。"
		resp.DryRunOnly = false
		resp.ExecutionMode = types.ControlExecutionPlaceholder
		return resp
	}
	resp.Result = types.ControlResultAccepted
	resp.HumanMessage = "动作已受理，但当前只接入 placeholder execution boundary，未执行真实系统动作。"
	resp.DryRunOnly = false
	resp.ExecutionMode = types.ControlExecutionPlaceholder
	resp.Preflight.BlockedReasons = append(resp.Preflight.BlockedReasons, types.ControlBlockedReason{
		Code:    types.ControlReasonPlaceholderOnly,
		Message: "当前还没有接入真实系统执行器。",
	})
	return resp
}

func populateNodeControlFacts(resp *types.ControlActionResponse, node types.NodeSummary) {
	resp.Facts["nodeStatus"] = node.Status
	resp.Facts["deploymentMode"] = node.DeploymentMode
	resp.Facts["serviceUnit"] = node.ServiceUnit
	resp.Facts["instanceProfile"] = node.InstanceProfile
	resp.Facts["instanceManaged"] = strconv.FormatBool(node.InstanceManaged)
	resp.Facts["isolated"] = strconv.FormatBool(node.Isolated)
}

func populateTunnelControlFacts(resp *types.ControlActionResponse, tunnel types.TunnelSpec) {
	resp.Facts["tunnelStatus"] = tunnel.Status
	resp.Facts["runtimeState"] = tunnel.RuntimeState
	resp.Facts["runtimePath"] = tunnel.RuntimePath
	resp.Facts["transportPolicy"] = tunnel.TransportPolicy
}

func addNodeSurfaceCheck(req types.ControlActionRequest, builder *controlPreflightBuilder) {
	switch req.SourceSurface {
	case types.ControlSurfaceNodeConsole:
		builder.addCheck("surface", "来源界面", types.ControlCheckPass, "当前来源为 node-console。", "")
	case types.ControlSurfaceOperatorConsole:
		builder.addCheck("surface", "来源界面", types.ControlCheckPass, "当前来源为 operator-console。", "")
	default:
		builder.addCheck("surface", "来源界面", types.ControlCheckBlocked, "当前来源界面不受支持。", types.ControlReasonUnsupportedSurface)
	}
}

func requiredCheck(ok bool) types.ControlCheckState {
	if ok {
		return types.ControlCheckPass
	}
	return types.ControlCheckBlocked
}

func ternaryMessage(ok bool, passMsg, failMsg string) string {
	if ok {
		return passMsg
	}
	return failMsg
}

func ternaryControlResult(ok bool, pass, fail types.ControlResult) types.ControlResult {
	if ok {
		return pass
	}
	return fail
}

func ternaryString(ok bool, pass, fail string) string {
	if ok {
		return pass
	}
	return fail
}

var (
	errInvalidControlPayload  = controlRequestError("invalid control action payload")
	errMissingControlTargetID = controlRequestError("targetId is required")
)

type controlRequestError string

func (e controlRequestError) Error() string {
	return string(e)
}
