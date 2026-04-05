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

type evaluatedControlAction struct {
	request  types.ControlActionRequest
	response types.ControlActionResponse
	status   int
}

type controlTargetSnapshot struct {
	targetKind        types.ControlTargetKind
	targetID          string
	surface           types.ControlSurface
	actions           []evaluatedControlAction
	executionMode     types.ControlExecutionMode
	placeholderOnly   bool
	recommendedAction types.ControlActionKind
	readinessState    types.ControlReadinessState
	primaryReasonCode types.ControlReasonCode
	blockedReasons    []types.ControlBlockedReason
	headline          string
	summary           string
	nextStep          string
	checks            []types.ControlCheckItem
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

func (s *Server) handleNodeControlPanel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	nodeID, surface, ok := parseControlOptionPath(r.URL.Path, "/api/control-panels/node/", "")
	if !ok {
		writeError(w, http.StatusNotFound, "control panel not found")
		return
	}
	resp, status := s.buildControlPanelSummary(r.Context(), types.ControlTargetNode, nodeID, surface)
	writeJSON(w, status, resp)
}

func (s *Server) handleTunnelControlPanel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	tunnelID, surface, ok := parseControlOptionPath(r.URL.Path, "/api/control-panels/tunnel/", "")
	if !ok {
		writeError(w, http.StatusNotFound, "control panel not found")
		return
	}
	resp, status := s.buildControlPanelSummary(r.Context(), types.ControlTargetTunnel, tunnelID, surface)
	writeJSON(w, status, resp)
}

func (s *Server) handleNodeControlActionOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	nodeID, surface, ok := parseControlOptionPath(r.URL.Path, "/api/control-actions/node/", "/options")
	if !ok {
		writeError(w, http.StatusNotFound, "control action options not found")
		return
	}
	resp, status := s.buildControlActionOptions(r.Context(), types.ControlTargetNode, nodeID, surface)
	writeJSON(w, status, resp)
}

func (s *Server) handleTunnelControlActionOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	tunnelID, surface, ok := parseControlOptionPath(r.URL.Path, "/api/control-actions/tunnel/", "/options")
	if !ok {
		writeError(w, http.StatusNotFound, "control action options not found")
		return
	}
	resp, status := s.buildControlActionOptions(r.Context(), types.ControlTargetTunnel, tunnelID, surface)
	writeJSON(w, status, resp)
}

func parseControlOptionPath(path, prefix, suffix string) (string, types.ControlSurface, bool) {
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	trimmed := strings.TrimPrefix(path, prefix)
	if suffix != "" {
		if !strings.HasSuffix(trimmed, suffix) {
			return "", "", false
		}
		trimmed = strings.TrimSuffix(trimmed, suffix)
	}
	trimmed = strings.Trim(trimmed, "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 {
		return "", "", false
	}
	id := strings.TrimSpace(parts[0])
	surface := types.ControlSurface(strings.TrimSpace(parts[1]))
	if id == "" || surface == "" {
		return "", "", false
	}
	return id, surface, true
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
		ActionKind:     req.ActionKind,
		TargetKind:     req.TargetKind,
		TargetID:       req.TargetID,
		SourceSurface:  req.SourceSurface,
		DryRunOnly:     req.DryRun,
		ExecutionMode:  types.ControlExecutionPlaceholder,
		ExecutionNotes: []types.ControlExecutionNote{},
		Facts:          map[string]string{},
	}
}

func (s *Server) buildControlActionOptions(ctx context.Context, targetKind types.ControlTargetKind, targetID string, surface types.ControlSurface) (types.ControlActionOptionsResponse, int) {
	snapshot, status := s.buildControlTargetSnapshot(ctx, targetKind, targetID, surface)
	if status != http.StatusOK {
		return types.ControlActionOptionsResponse{}, status
	}
	resp := types.ControlActionOptionsResponse{
		TargetKind:    targetKind,
		TargetID:      targetID,
		SourceSurface: surface,
		ExecutionMode: snapshot.executionMode,
		Items:         []types.ControlActionOption{},
	}
	for _, evaluated := range snapshot.actions {
		resp.Items = append(resp.Items, controlActionOptionFromEvaluated(evaluated))
	}
	return resp, http.StatusOK
}

func (s *Server) buildControlPanelSummary(ctx context.Context, targetKind types.ControlTargetKind, targetID string, surface types.ControlSurface) (types.ControlPanelSummary, int) {
	snapshot, status := s.buildControlTargetSnapshot(ctx, targetKind, targetID, surface)
	if status != http.StatusOK {
		return types.ControlPanelSummary{}, status
	}
	return types.ControlPanelSummary{
		TargetKind:        targetKind,
		TargetID:          targetID,
		SourceSurface:     surface,
		Headline:          snapshot.headline,
		Summary:           snapshot.summary,
		ReadinessState:    snapshot.readinessState,
		Checks:            append([]types.ControlCheckItem{}, snapshot.checks...),
		PrimaryReasonCode: snapshot.primaryReasonCode,
		NextStep:          snapshot.nextStep,
		RecommendedAction: snapshot.recommendedAction,
		ExecutionMode:     snapshot.executionMode,
		PlaceholderOnly:   snapshot.placeholderOnly,
	}, http.StatusOK
}

func (s *Server) buildControlTargetSnapshot(ctx context.Context, targetKind types.ControlTargetKind, targetID string, surface types.ControlSurface) (controlTargetSnapshot, int) {
	snapshot := controlTargetSnapshot{
		targetKind:      targetKind,
		targetID:        targetID,
		surface:         surface,
		actions:         []evaluatedControlAction{},
		executionMode:   types.ControlExecutionPlaceholder,
		placeholderOnly: true,
		blockedReasons:  []types.ControlBlockedReason{},
		checks:          []types.ControlCheckItem{},
	}
	actions := allowedActionsForTarget(targetKind)
	if len(actions) == 0 {
		return snapshot, http.StatusBadRequest
	}
	for _, actionKind := range actions {
		resp, status := s.evaluateControlAction(ctx, types.ControlActionRequest{
			ActionKind:    actionKind,
			TargetKind:    targetKind,
			TargetID:      targetID,
			SourceSurface: surface,
			DryRun:        true,
			RequestedAt:   time.Now().UTC(),
		})
		if status != http.StatusOK {
			return snapshot, status
		}
		snapshot.actions = append(snapshot.actions, evaluatedControlAction{
			request: types.ControlActionRequest{
				ActionKind:    actionKind,
				TargetKind:    targetKind,
				TargetID:      targetID,
				SourceSurface: surface,
				DryRun:        true,
			},
			response: resp,
			status:   status,
		})
	}
	snapshot.recommendedAction = recommendedActionForSnapshot(targetKind, snapshot.actions)
	snapshot.checks, snapshot.blockedReasons = aggregateTargetChecks(targetKind, snapshot.actions, snapshot.recommendedAction)
	snapshot.primaryReasonCode = firstPrimaryReason(snapshot.blockedReasons)
	snapshot.headline, snapshot.summary, snapshot.nextStep, snapshot.readinessState = summarizeSnapshot(snapshot)
	return snapshot, http.StatusOK
}

func summarizeSnapshot(snapshot controlTargetSnapshot) (string, string, string, types.ControlReadinessState) {
	if len(snapshot.checks) == 0 {
		return "当前控制前提已阻断。", "当前没有可用的控制检查。", "请重新读取控制摘要。", types.ControlReadinessBlocked
	}
	if snapshot.primaryReasonCode == "" {
		return "当前控制前提已满足。", "当前主要控制前提已经满足，推荐动作仍然只会进入 placeholder execute。", "可先执行 dry-run 或占位执行，确认交互边界。", types.ControlReadinessReady
	}
	primaryMessage := firstBlockedMessage(snapshot.blockedReasons)
	if containsMissingCheck(snapshot.checks) {
		return "当前控制前提部分缺失。", primaryMessage, nextStepForReason(snapshot.primaryReasonCode), types.ControlReadinessPartial
	}
	return "当前控制前提已阻断。", primaryMessage, nextStepForReason(snapshot.primaryReasonCode), types.ControlReadinessBlocked
}

func aggregateTargetChecks(targetKind types.ControlTargetKind, actions []evaluatedControlAction, recommendedAction types.ControlActionKind) ([]types.ControlCheckItem, []types.ControlBlockedReason) {
	if len(actions) == 0 {
		return nil, nil
	}
	switch targetKind {
	case types.ControlTargetNode:
		return aggregateNodeTargetChecks(actions)
	case types.ControlTargetTunnel:
		return aggregateTunnelTargetChecks(actions, recommendedAction)
	default:
		return nil, nil
	}
}

func aggregateNodeTargetChecks(actions []evaluatedControlAction) ([]types.ControlCheckItem, []types.ControlBlockedReason) {
	byAction := map[types.ControlActionKind]types.ControlActionResponse{}
	for _, action := range actions {
		byAction[action.request.ActionKind] = action.response
	}
	restart := byAction[types.ControlActionRestartAgent]
	release := byAction[types.ControlActionReleaseNode]
	checks := []types.ControlCheckItem{
		findCheckItem(restart.Preflight.Items, "surface"),
		findCheckItem(restart.Preflight.Items, "online"),
		findCheckItem(restart.Preflight.Items, "managed"),
		findCheckItem(restart.Preflight.Items, "deployment"),
		findCheckItem(restart.Preflight.Items, "serviceUnit"),
		findCheckItem(restart.Preflight.Items, "instanceProfile"),
	}
	isolation := types.ControlCheckItem{Code: "isolation", Label: "节点当前隔离状态", State: types.ControlCheckPass, Message: "当前节点未隔离。"}
	if hasBlockedReason(restart.Preflight.BlockedReasons, types.ControlReasonNodeIsolated) || release.Result == types.ControlResultAccepted {
		isolation = types.ControlCheckItem{Code: "isolation", Label: "节点当前隔离状态", State: types.ControlCheckBlocked, Message: "当前节点已隔离。"}
	}
	checks = append(checks, isolation)
	blockedReasons := []types.ControlBlockedReason{}
	for _, code := range []types.ControlReasonCode{types.ControlReasonNodeOffline, types.ControlReasonUnmanagedInstance, types.ControlReasonMissingDeploymentMode, types.ControlReasonMissingServiceUnit, types.ControlReasonMissingInstanceProfile, types.ControlReasonNodeIsolated} {
		if reason, ok := firstReasonByCode(restart.Preflight.BlockedReasons, code); ok {
			blockedReasons = append(blockedReasons, reason)
		}
	}
	return checks, blockedReasons
}

func aggregateTunnelTargetChecks(actions []evaluatedControlAction, recommendedAction types.ControlActionKind) ([]types.ControlCheckItem, []types.ControlBlockedReason) {
	byAction := map[types.ControlActionKind]types.ControlActionResponse{}
	for _, action := range actions {
		byAction[action.request.ActionKind] = action.response
	}
	recommended := byAction[recommendedAction]
	checks := []types.ControlCheckItem{
		findCheckItem(recommended.Preflight.Items, "surface"),
		findCheckItem(recommended.Preflight.Items, "tunnelStatus"),
	}
	statusConflict := types.ControlCheckItem{Code: "statusConflict", Label: "pause/resume 状态匹配性", State: types.ControlCheckPass, Message: "当前 tunnel 状态与推荐动作匹配。"}
	if reason, ok := firstReasonByCode(recommended.Preflight.BlockedReasons, types.ControlReasonTunnelStateConflict); ok {
		statusConflict = types.ControlCheckItem{Code: "statusConflict", Label: "pause/resume 状态匹配性", State: types.ControlCheckBlocked, Message: reason.Message}
	}
	checks = append(checks, statusConflict)
	blockedReasons := []types.ControlBlockedReason{}
	for _, code := range []types.ControlReasonCode{types.ControlReasonTargetNotFound, types.ControlReasonUnsupportedSurface, types.ControlReasonTunnelStateConflict} {
		if reason, ok := firstReasonByCode(recommended.Preflight.BlockedReasons, code); ok {
			blockedReasons = append(blockedReasons, reason)
		}
	}
	return checks, blockedReasons
}

func findCheckItem(items []types.ControlCheckItem, code string) types.ControlCheckItem {
	for _, item := range items {
		if item.Code == code {
			return item
		}
	}
	return types.ControlCheckItem{Code: code, Label: code, State: types.ControlCheckMissing, Message: "当前还没有上报 " + code + "。"}
}

func firstReasonByCode(reasons []types.ControlBlockedReason, code types.ControlReasonCode) (types.ControlBlockedReason, bool) {
	for _, reason := range reasons {
		if reason.Code == code {
			return reason, true
		}
	}
	return types.ControlBlockedReason{}, false
}

func hasBlockedReason(reasons []types.ControlBlockedReason, code types.ControlReasonCode) bool {
	_, ok := firstReasonByCode(reasons, code)
	return ok
}

func recommendedActionForSnapshot(targetKind types.ControlTargetKind, actions []evaluatedControlAction) types.ControlActionKind {
	switch targetKind {
	case types.ControlTargetNode:
		if actionBlockedForReason(actions, types.ControlActionRestartAgent, types.ControlReasonNodeIsolated) || actionAllowed(actions, types.ControlActionReleaseNode) {
			if hasAction(actions, types.ControlActionReleaseNode) {
				return types.ControlActionReleaseNode
			}
		}
		if actionAllowed(actions, types.ControlActionRestartAgent) {
			return types.ControlActionRestartAgent
		}
		if actionAllowed(actions, types.ControlActionIsolateNode) {
			return types.ControlActionIsolateNode
		}
		if hasAction(actions, types.ControlActionRestartAgent) {
			return types.ControlActionRestartAgent
		}
		if hasAction(actions, types.ControlActionReleaseNode) {
			return types.ControlActionReleaseNode
		}
		return types.ControlActionRestartAgent
	case types.ControlTargetTunnel:
		if actionAllowed(actions, types.ControlActionResumeTunnel) {
			return types.ControlActionResumeTunnel
		}
		if actionAllowed(actions, types.ControlActionPauseTunnel) {
			return types.ControlActionPauseTunnel
		}
		if hasAction(actions, types.ControlActionResumeTunnel) {
			return types.ControlActionResumeTunnel
		}
		return types.ControlActionPauseTunnel
	default:
		return ""
	}
}

func actionBlockedForReason(actions []evaluatedControlAction, actionKind types.ControlActionKind, reason types.ControlReasonCode) bool {
	for _, action := range actions {
		if action.request.ActionKind != actionKind {
			continue
		}
		for _, blocked := range action.response.Preflight.BlockedReasons {
			if blocked.Code == reason {
				return true
			}
		}
	}
	return false
}

func actionAllowed(actions []evaluatedControlAction, actionKind types.ControlActionKind) bool {
	for _, action := range actions {
		if action.request.ActionKind == actionKind && len(action.response.Preflight.BlockedReasons) == 0 {
			return true
		}
	}
	return false
}

func hasAction(actions []evaluatedControlAction, actionKind types.ControlActionKind) bool {
	for _, action := range actions {
		if action.request.ActionKind == actionKind {
			return true
		}
	}
	return false
}

func findEvaluatedAction(actions []evaluatedControlAction, actionKind types.ControlActionKind) *evaluatedControlAction {
	for index := range actions {
		if actions[index].request.ActionKind == actionKind {
			return &actions[index]
		}
	}
	return nil
}

func firstBlockedMessage(reasons []types.ControlBlockedReason) string {
	if len(reasons) > 0 {
		return reasons[0].Message
	}
	return "当前控制前提已阻断。"
}

func allowedActionsForTarget(targetKind types.ControlTargetKind) []types.ControlActionKind {
	switch targetKind {
	case types.ControlTargetNode:
		return []types.ControlActionKind{types.ControlActionRestartAgent, types.ControlActionIsolateNode, types.ControlActionReleaseNode}
	case types.ControlTargetTunnel:
		return []types.ControlActionKind{types.ControlActionPauseTunnel, types.ControlActionResumeTunnel}
	default:
		return nil
	}
}

func controlActionOptionFromEvaluated(evaluated evaluatedControlAction) types.ControlActionOption {
	result := evaluated.response
	if result.Result == types.ControlResultAccepted {
		summary, nextStep := controlOptionSummaryAndNextStep(result, nil)
		return types.ControlActionOption{
			ActionKind:        result.ActionKind,
			TargetKind:        result.TargetKind,
			TargetID:          result.TargetID,
			SourceSurface:     result.SourceSurface,
			Available:         true,
			AvailabilityState: types.ControlAvailabilityPlaceholderOnly,
			Label:             controlActionLabel(result.ActionKind),
			Message:           result.HumanMessage,
			Summary:           summary,
			NextStep:          nextStep,
			ExecutionMode:     types.ControlExecutionPlaceholder,
			PlaceholderOnly:   true,
			ExecutionNotes:    []types.ControlExecutionNote{{Code: types.ControlReasonPlaceholderOnly, Message: "当前只会进入 placeholder execute。"}},
		}
	}
	var primaryReason types.ControlReasonCode
	if len(result.Preflight.BlockedReasons) > 0 {
		primaryReason = result.Preflight.BlockedReasons[0].Code
	}
	summary, nextStep := controlOptionSummaryAndNextStep(result, result.Preflight.BlockedReasons)
	return types.ControlActionOption{
		ActionKind:        result.ActionKind,
		TargetKind:        result.TargetKind,
		TargetID:          result.TargetID,
		SourceSurface:     result.SourceSurface,
		Available:         false,
		AvailabilityState: types.ControlAvailabilityBlocked,
		Label:             controlActionLabel(result.ActionKind),
		Message:           result.HumanMessage,
		Summary:           summary,
		NextStep:          nextStep,
		PrimaryReasonCode: primaryReason,
		ExecutionMode:     types.ControlExecutionPlaceholder,
		PlaceholderOnly:   false,
		ReasonHints:       append([]types.ControlBlockedReason{}, result.Preflight.BlockedReasons...),
	}
}

func controlOptionSummaryAndNextStep(result types.ControlActionResponse, reasons []types.ControlBlockedReason) (string, string) {
	if result.Result == types.ControlResultAccepted {
		return "当前动作可以发起，但本轮只会进入占位执行边界。", "先执行 dry-run 或占位执行确认交互，再等待后续真实执行器接入。"
	}
	if len(reasons) == 0 {
		return "当前动作已被阻断。", "先补齐目标状态或来源界面条件，再重新读取动作摘要。"
	}
	return reasons[0].Message, nextStepForReason(reasons[0].Code)
}

func controlActionLabel(actionKind types.ControlActionKind) string {
	switch actionKind {
	case types.ControlActionRestartAgent:
		return "restart_agent"
	case types.ControlActionIsolateNode:
		return "isolate_node"
	case types.ControlActionReleaseNode:
		return "release_node"
	case types.ControlActionPauseTunnel:
		return "pause_tunnel"
	case types.ControlActionResumeTunnel:
		return "resume_tunnel"
	default:
		return string(actionKind)
	}
}

func nextStepForReason(code types.ControlReasonCode) string {
	switch code {
	case types.ControlReasonNodeOffline:
		return "先恢复节点在线状态，再重新读取控制摘要。"
	case types.ControlReasonNodeIsolated:
		return "先确认是否应 release 节点，或改为使用适合隔离状态的动作。"
	case types.ControlReasonNodeNotIsolated:
		return "如需 release，先确认节点已经进入隔离状态。"
	case types.ControlReasonMissingServiceUnit:
		return "先补齐 serviceUnit 上报，再重新读取控制摘要。"
	case types.ControlReasonMissingInstanceProfile:
		return "先补齐 instanceProfile 上报，再重新读取控制摘要。"
	case types.ControlReasonMissingDeploymentMode:
		return "先补齐 deploymentMode 上报，再重新读取控制摘要。"
	case types.ControlReasonUnmanagedInstance:
		return "先把实例切回受管模式或补齐受管实例信息。"
	case types.ControlReasonUnsupportedSurface:
		return "请切换到允许该动作的 console 或目标上下文后再重试。"
	case types.ControlReasonTunnelStateConflict:
		return "先确认 tunnel 当前 status，再选择匹配的 pause/resume 动作。"
	default:
		return "先处理首个阻断原因，再重新读取控制摘要。"
	}
}

func firstPrimaryReason(reasons []types.ControlBlockedReason) types.ControlReasonCode {
	if len(reasons) == 0 {
		return ""
	}
	return reasons[0].Code
}

func containsMissingCheck(items []types.ControlCheckItem) bool {
	for _, item := range items {
		if item.State == types.ControlCheckMissing {
			return true
		}
	}
	return false
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
	builder.addCheck("managed", "受管实例", missingOrPass(node.InstanceManaged), ternaryMessage(node.InstanceManaged, "当前节点为受管实例。", "当前节点没有上报为受管实例。"), types.ControlReasonUnmanagedInstance)
	builder.addCheck("deployment", "deploymentMode", missingOrPass(strings.TrimSpace(node.DeploymentMode) != ""), ternaryMessage(strings.TrimSpace(node.DeploymentMode) != "", node.DeploymentMode, "当前还没有上报 deploymentMode。"), types.ControlReasonMissingDeploymentMode)
	builder.addCheck("serviceUnit", "serviceUnit 已上报", missingOrPass(strings.TrimSpace(node.ServiceUnit) != ""), ternaryMessage(strings.TrimSpace(node.ServiceUnit) != "", node.ServiceUnit, "当前还没有上报 serviceUnit。"), types.ControlReasonMissingServiceUnit)
	builder.addCheck("instanceProfile", "instanceProfile 已上报", missingOrPass(strings.TrimSpace(node.InstanceProfile) != ""), ternaryMessage(strings.TrimSpace(node.InstanceProfile) != "", node.InstanceProfile, "当前还没有上报 instanceProfile。"), types.ControlReasonMissingInstanceProfile)

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
	enforceMissingChecksAsBlocked(builder)
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
	builder.addCheck("tunnelStatus", "tunnel status", ternaryTunnelCheck(tunnel.Status), ternaryMessage(tunnel.Status != "", "当前 tunnel status="+tunnel.Status+"。", "当前 tunnel 尚未上报 status。"), types.ControlReasonTargetNotFound)

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

func ternaryTunnelCheck(status string) types.ControlCheckState {
	if strings.TrimSpace(status) == "" {
		return types.ControlCheckMissing
	}
	return types.ControlCheckPass
}

func (s *Server) executePlaceholderControlAction(resp types.ControlActionResponse) types.ControlActionResponse {
	if !resp.Preflight.Allowed {
		resp.Result = types.ControlResultBlocked
		resp.HumanMessage = "execute 已被预检阻断。"
		resp.DryRunOnly = false
		resp.ExecutionMode = types.ControlExecutionPlaceholder
		resp.PlaceholderOnly = false
		return resp
	}
	resp.Result = types.ControlResultAccepted
	resp.HumanMessage = "动作已受理，但当前只接入 placeholder execution boundary，未执行真实系统动作。"
	resp.DryRunOnly = false
	resp.ExecutionMode = types.ControlExecutionPlaceholder
	resp.PlaceholderOnly = true
	resp.ExecutionNotes = append(resp.ExecutionNotes, types.ControlExecutionNote{
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

func enforceMissingChecksAsBlocked(builder *controlPreflightBuilder) {
	for _, item := range builder.summary.Items {
		if item.State != types.ControlCheckMissing {
			continue
		}
		reasonCode, ok := missingReasonCodeForCheck(item.Code)
		if !ok {
			continue
		}
		builder.block(reasonCode, item.Message)
	}
}

func missingReasonCodeForCheck(code string) (types.ControlReasonCode, bool) {
	switch code {
	case "managed":
		return types.ControlReasonUnmanagedInstance, true
	case "deployment":
		return types.ControlReasonMissingDeploymentMode, true
	case "serviceUnit":
		return types.ControlReasonMissingServiceUnit, true
	case "instanceProfile":
		return types.ControlReasonMissingInstanceProfile, true
	default:
		return "", false
	}
}

func requiredCheck(ok bool) types.ControlCheckState {
	if ok {
		return types.ControlCheckPass
	}
	return types.ControlCheckBlocked
}

func missingOrPass(ok bool) types.ControlCheckState {
	if ok {
		return types.ControlCheckPass
	}
	return types.ControlCheckMissing
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
