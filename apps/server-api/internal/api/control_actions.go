package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/25743/cloud-relay-platform/apps/server-api/internal/store"
	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

type controlPreflightBuilder struct {
	summary types.ControlPreflightSummary
}

type evaluatedControlAction struct {
	request  types.ControlActionRequest
	response types.ControlActionResponse
	execute  controlExecutionResult
	status   int
}

type controlTargetSnapshot struct {
	targetKind        types.ControlTargetKind
	targetID          string
	surface           types.ControlSurface
	contextVersion    string
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

type controlExecutionPlan struct {
	actionKind        types.ControlActionKind
	targetKind        types.ControlTargetKind
	targetID          string
	sourceSurface     types.ControlSurface
	executionMode     types.ControlExecutionMode
	placeholderOnly   bool
	requestedAt       time.Time
	note              string
	previewFacts      map[string]string
	targetFacts       map[string]string
	preflight         types.ControlPreflightSummary
	primaryReasonCode types.ControlReasonCode
	blockingReasons   []types.ControlBlockedReason
	readinessState    types.ControlReadinessState
	recommendedAction types.ControlActionKind
	actionEvaluation  evaluatedControlAction
}

type controlExecutionRevalidation struct {
	allowed           bool
	outcome           controlExecutionOutcome
	humanMessage      string
	executionMode     types.ControlExecutionMode
	executionNotes    []types.ControlExecutionNote
	blockingReasons   []types.ControlBlockedReason
	primaryReason     types.ControlReasonCode
	targetFacts       map[string]string
	preflight         types.ControlPreflightSummary
	recommendedAction types.ControlActionKind
	readinessState    types.ControlReadinessState
}

type controlExecutionOutcome string

const (
	controlExecutionOutcomeAcceptedReal        controlExecutionOutcome = "accepted_real"
	controlExecutionOutcomeAcceptedPlaceholder controlExecutionOutcome = "accepted_placeholder"
	controlExecutionOutcomeBlockedPreflight    controlExecutionOutcome = "blocked_preflight"
	controlExecutionOutcomePolicyRejected      controlExecutionOutcome = "policy_rejected"
	controlExecutionOutcomeRetryableFailure    controlExecutionOutcome = "retryable_failure"
	controlExecutionOutcomeNonRetryableFailure controlExecutionOutcome = "non_retryable_failure"
)

const defaultRestartServicePrefix = "cloud-relay-client-agent@"

type restartAgentExecutorConfig struct {
	enabled              bool
	localNodeID          string
	allowedServicePrefix string
	timeout              time.Duration
}

type controlCommandResult struct {
	exitCode int
	stdout   string
	stderr   string
	err      error
}

type restartAgentRunner interface {
	restartService(ctx context.Context, serviceUnit string) controlCommandResult
}

type controlExecutionResult struct {
	outcome        controlExecutionOutcome
	humanMessage   string
	executionMode  types.ControlExecutionMode
	executionNotes []types.ControlExecutionNote
	auditHint      string
}

type controlExecutor interface {
	// execute only handles already-allowed execute paths. Blocked and dry-run
	// requests are resolved in evaluateControlAction before the executor seam.
	// Executors may still classify allowed executions as accepted, policy-
	// rejected, or failed without changing the external HTTP response schema.
	execute(ctx context.Context, plan controlExecutionPlan) controlExecutionResult
}

type restartCapableControlExecutor struct {
	placeholder controlExecutor
	restart     restartAgentExecutor
}

type stateMutationControlExecutor struct {
	store    store.Store
	fallback controlExecutor
}

type restartAgentExecutor struct {
	config restartAgentExecutorConfig
	runner restartAgentRunner
}

type placeholderControlExecutor struct{}

type systemctlRestartRunner struct{}

func newPlaceholderControlExecutor() controlExecutor {
	return placeholderControlExecutor{}
}

func newConfiguredControlExecutor() controlExecutor {
	config := restartAgentExecutorConfig{
		enabled:              parseBoolEnv(envOrDefault("SERVER_API_CONTROL_REAL_RESTART_ENABLED", "false")),
		localNodeID:          strings.TrimSpace(envOrDefault("SERVER_API_CONTROL_LOCAL_NODE_ID", "")),
		allowedServicePrefix: strings.TrimSpace(envOrDefault("SERVER_API_CONTROL_RESTART_SERVICE_PREFIX", defaultRestartServicePrefix)),
		timeout:              time.Duration(parsePositiveInt(envOrDefault("SERVER_API_CONTROL_RESTART_TIMEOUT_SEC", "15"), 15)) * time.Second,
	}
	fallback := newPlaceholderControlExecutor()
	if config.enabled {
		fallback = newRestartCapableControlExecutor(config, systemctlRestartRunner{})
	}
	return newStateMutationControlExecutor(nil, fallback)
}

func newRestartCapableControlExecutor(config restartAgentExecutorConfig, runner restartAgentRunner) controlExecutor {
	if strings.TrimSpace(config.allowedServicePrefix) == "" {
		config.allowedServicePrefix = defaultRestartServicePrefix
	}
	if config.timeout <= 0 {
		config.timeout = 15 * time.Second
	}
	if runner == nil {
		runner = systemctlRestartRunner{}
	}
	return restartCapableControlExecutor{
		placeholder: newPlaceholderControlExecutor(),
		restart: restartAgentExecutor{
			config: config,
			runner: runner,
		},
	}
}

func newStateMutationControlExecutor(backend store.Store, fallback controlExecutor) controlExecutor {
	if fallback == nil {
		fallback = newPlaceholderControlExecutor()
	}
	if backend == nil {
		return fallback
	}
	return stateMutationControlExecutor{store: backend, fallback: fallback}
}

func (e restartCapableControlExecutor) execute(ctx context.Context, plan controlExecutionPlan) controlExecutionResult {
	if result, handled := e.restart.execute(ctx, plan); handled {
		return result
	}
	return e.placeholder.execute(ctx, plan)
}

func (e stateMutationControlExecutor) execute(ctx context.Context, plan controlExecutionPlan) controlExecutionResult {
	if result, handled := e.executeStateMutation(ctx, plan); handled {
		return result
	}
	return e.fallback.execute(ctx, plan)
}

func (e stateMutationControlExecutor) executeStateMutation(ctx context.Context, plan controlExecutionPlan) (controlExecutionResult, bool) {
	switch plan.actionKind {
	case types.ControlActionIsolateNode:
		return e.executeNodeIsolation(ctx, plan, true), true
	case types.ControlActionReleaseNode:
		return e.executeNodeIsolation(ctx, plan, false), true
	case types.ControlActionPauseTunnel:
		return e.executeTunnelStatus(ctx, plan, "paused"), true
	case types.ControlActionResumeTunnel:
		return e.executeTunnelStatus(ctx, plan, "active"), true
	default:
		return controlExecutionResult{}, false
	}
}

func (e stateMutationControlExecutor) executeNodeIsolation(ctx context.Context, plan controlExecutionPlan, isolated bool) controlExecutionResult {
	current, err := e.store.GetNode(ctx, plan.targetID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nonRetryableFailureExecutionResult(plan, "节点在 execute 阶段不存在，无法更新隔离状态。")
		}
		return retryableFailureExecutionResult(plan, "读取节点当前状态失败，可稍后重试。详情: "+err.Error())
	}
	if current.Isolated == isolated {
		if isolated {
			return policyRejectedExecutionResult(plan, "节点当前已经是 isolated 状态，不再允许重复 isolate。")
		}
		return policyRejectedExecutionResult(plan, "节点当前已经是 released 状态，不再允许重复 release。")
	}
	updated, err := e.store.UpdateNode(ctx, store.UpdateNodeParams{
		NodeID:      current.NodeID,
		NodeRole:    current.NodeRole,
		Environment: current.Environment,
		TrustLevel:  current.TrustLevel,
		Owner:       current.Owner,
		Location:    current.Location,
		Tags:        current.Tags,
		Isolated:    isolated,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nonRetryableFailureExecutionResult(plan, "节点在更新隔离状态时丢失，无法继续执行。")
		}
		return retryableFailureExecutionResult(plan, "更新节点隔离状态失败，可稍后重试。详情: "+err.Error())
	}
	message := "已真实更新节点隔离状态。"
	note := "节点已更新为 isolated=false。"
	if updated.Isolated {
		message = "已真实隔离目标节点。"
		note = "节点已更新为 isolated=true。"
	}
	return acceptedStateMutationExecutionResult(message, note)
}

func (e stateMutationControlExecutor) executeTunnelStatus(ctx context.Context, plan controlExecutionPlan, status string) controlExecutionResult {
	current, err := e.store.GetTunnel(ctx, plan.targetID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nonRetryableFailureExecutionResult(plan, "tunnel 在 execute 阶段不存在，无法更新状态。")
		}
		return retryableFailureExecutionResult(plan, "读取 tunnel 当前状态失败，可稍后重试。详情: "+err.Error())
	}
	if current.Status == status {
		if status == "paused" {
			return policyRejectedExecutionResult(plan, "tunnel 当前已经是 paused 状态，不再允许重复 pause。")
		}
		return policyRejectedExecutionResult(plan, "tunnel 当前已经是 active 状态，不再允许重复 resume。")
	}
	current.Status = status
	updated, err := e.store.UpdateTunnel(ctx, current)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nonRetryableFailureExecutionResult(plan, "tunnel 在更新状态时丢失，无法继续执行。")
		}
		return retryableFailureExecutionResult(plan, "更新 tunnel 状态失败，可稍后重试。详情: "+err.Error())
	}
	message := "已真实更新 tunnel 状态。"
	note := "tunnel status 已更新为 " + updated.Status + "。"
	if updated.Status == "paused" {
		message = "已真实暂停 tunnel。"
	}
	if updated.Status == "active" {
		message = "已真实恢复 tunnel。"
	}
	return acceptedStateMutationExecutionResult(message, note)
}

func (e restartAgentExecutor) execute(ctx context.Context, plan controlExecutionPlan) (controlExecutionResult, bool) {
	if plan.actionKind != types.ControlActionRestartAgent || plan.targetKind != types.ControlTargetNode {
		return controlExecutionResult{}, false
	}
	if !e.config.enabled {
		return controlExecutionResult{}, false
	}
	if plan.sourceSurface != types.ControlSurfaceNodeConsole {
		return controlExecutionResult{}, false
	}
	if strings.TrimSpace(e.config.localNodeID) == "" || plan.targetID != e.config.localNodeID {
		return controlExecutionResult{}, false
	}
	serviceUnit := strings.TrimSpace(plan.targetFacts["serviceUnit"])
	if rejected, ok := validateRestartAgentExecutionPolicy(plan, e.config.allowedServicePrefix, serviceUnit); ok {
		return rejected, true
	}
	execCtx, cancel := context.WithTimeout(ctx, e.config.timeout)
	defer cancel()
	commandResult := e.runner.restartService(execCtx, serviceUnit)
	return classifyRestartAgentCommandResult(plan, serviceUnit, commandResult), true
}

func (systemctlRestartRunner) restartService(ctx context.Context, serviceUnit string) controlCommandResult {
	cmd := exec.CommandContext(ctx, "systemctl", "restart", serviceUnit)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := controlCommandResult{
		stdout: strings.TrimSpace(stdout.String()),
		stderr: strings.TrimSpace(stderr.String()),
		err:    err,
	}
	if err == nil {
		result.exitCode = 0
		return result
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.exitCode = exitErr.ExitCode()
	} else {
		result.exitCode = -1
	}
	return result
}

func (placeholderControlExecutor) execute(_ context.Context, plan controlExecutionPlan) controlExecutionResult {
	return acceptedPlaceholderExecutionResult(plan)
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

func (s *Server) evaluateControlActionDryRun(ctx context.Context, req types.ControlActionRequest) (types.ControlActionResponse, int) {
	resp := newControlActionResponse(req)
	preflight, status := s.evaluateControlPreflight(ctx, req, &resp)
	resp.Preflight = preflight
	if status != http.StatusOK {
		return resp, status
	}
	resp.Result = ternaryControlResult(preflight.Allowed, types.ControlResultAccepted, types.ControlResultBlocked)
	resp.HumanMessage = ternaryString(preflight.Allowed, "preflight 已完成，当前只执行 dry-run。", "preflight 已阻断，当前不允许进入 execute。")
	resp.DryRunOnly = true
	resp.ExecutionMode = types.ControlExecutionPlaceholder
	return resp, http.StatusOK
}

func (s *Server) evaluateControlAction(ctx context.Context, req types.ControlActionRequest) (types.ControlActionResponse, int) {
	snapshot, status := s.buildControlTargetSnapshot(ctx, req.TargetKind, req.TargetID, req.SourceSurface)
	if status != http.StatusOK {
		resp := newControlActionResponse(req)
		if status == http.StatusNotFound {
			resp.Result = types.ControlResultBlocked
			resp.HumanMessage = targetNotFoundMessage(req.TargetKind)
			resp.Preflight = types.ControlPreflightSummary{
				Allowed: false,
				Items:   []types.ControlCheckItem{},
				BlockedReasons: []types.ControlBlockedReason{{
					Code:    types.ControlReasonTargetNotFound,
					Message: targetNotFoundMessage(req.TargetKind),
				}},
			}
		}
		return resp, status
	}
	actionSnapshot := findActionSnapshot(snapshot, req.ActionKind)
	if actionSnapshot == nil {
		resp := newControlActionResponse(req)
		resp.Result = types.ControlResultBlocked
		resp.HumanMessage = "当前动作不在本轮白名单内。"
		resp.Preflight = types.ControlPreflightSummary{
			Allowed: false,
			Items:   []types.ControlCheckItem{},
			BlockedReasons: []types.ControlBlockedReason{{
				Code:    types.ControlReasonUnsupportedAction,
				Message: "当前动作不在本轮白名单内。",
			}},
		}
		return resp, http.StatusOK
	}
	resp := cloneControlActionResponse(actionSnapshot.response)
	resp.DryRunOnly = req.DryRun
	resp.ExecutionMode = snapshot.executionMode
	resp.PlaceholderOnly = false
	resp.ExecutionNotes = []types.ControlExecutionNote{}
	resp.Facts = cloneFacts(resp.Facts)
	resp.Preflight = clonePreflightSummary(resp.Preflight)
	resp.SourceSurface = req.SourceSurface
	resp.TargetID = req.TargetID
	resp.TargetKind = req.TargetKind
	resp.ActionKind = req.ActionKind
	if req.DryRun {
		resp.DryRunOnly = true
		resp.ExecutionMode = snapshot.executionMode
		return resp, http.StatusOK
	}
	if req.RequestedAt.IsZero() {
		if requestedAt, ok := parseControlStateTimestamp(snapshot.contextVersion); ok {
			req.RequestedAt = requestedAt
		}
	}
	plan := buildExecutionPlan(req, snapshot, *actionSnapshot)
	if revalidated := revalidateRequestedControlContext(plan); !revalidated.allowed {
		plan = applyExecutionRevalidation(plan, revalidated)
		result := controlExecutionResult{
			outcome:        revalidated.outcome,
			humanMessage:   revalidated.humanMessage,
			executionMode:  revalidated.executionMode,
			executionNotes: cloneExecutionNotes(revalidated.executionNotes),
			auditHint:      "state_drift",
		}
		resp = buildControlActionResponse(req, plan, result)
		s.writeControlExecutionAudit(ctx, req, plan, result, resp)
		return resp, http.StatusOK
	}
	if !plan.preflight.Allowed {
		return buildControlActionResponse(req, plan, blockedExecutionResult(plan)), http.StatusOK
	}
	result := s.controlExecutor.execute(ctx, plan)
	resp = buildControlActionResponse(req, plan, result)
	s.writeControlExecutionAudit(ctx, req, plan, result, resp)
	return resp, http.StatusOK
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

func targetNotFoundMessage(targetKind types.ControlTargetKind) string {
	if targetKind == types.ControlTargetTunnel {
		return "未找到目标 tunnel。"
	}
	return "未找到目标节点。"
}

func cloneControlActionResponse(in types.ControlActionResponse) types.ControlActionResponse {
	return types.ControlActionResponse{
		Result:          in.Result,
		ActionKind:      in.ActionKind,
		TargetKind:      in.TargetKind,
		TargetID:        in.TargetID,
		SourceSurface:   in.SourceSurface,
		Preflight:       clonePreflightSummary(in.Preflight),
		HumanMessage:    in.HumanMessage,
		DryRunOnly:      in.DryRunOnly,
		ExecutionMode:   in.ExecutionMode,
		PlaceholderOnly: in.PlaceholderOnly,
		ExecutionNotes:  append([]types.ControlExecutionNote{}, in.ExecutionNotes...),
		Facts:           cloneFacts(in.Facts),
	}
}

func clonePreflightSummary(in types.ControlPreflightSummary) types.ControlPreflightSummary {
	return types.ControlPreflightSummary{
		Allowed:        in.Allowed,
		Items:          append([]types.ControlCheckItem{}, in.Items...),
		BlockedReasons: append([]types.ControlBlockedReason{}, in.BlockedReasons...),
	}
}

func cloneFacts(in map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func buildExecutionPlan(req types.ControlActionRequest, snapshot controlTargetSnapshot, action evaluatedControlAction) controlExecutionPlan {
	plan := controlExecutionPlan{
		actionKind:        req.ActionKind,
		targetKind:        req.TargetKind,
		targetID:          req.TargetID,
		sourceSurface:     req.SourceSurface,
		executionMode:     snapshot.executionMode,
		placeholderOnly:   snapshot.placeholderOnly,
		requestedAt:       req.RequestedAt,
		note:              req.Note,
		previewFacts:      cloneFacts(action.response.Facts),
		targetFacts:       cloneFacts(action.response.Facts),
		preflight:         clonePreflightSummary(action.response.Preflight),
		primaryReasonCode: firstPrimaryReason(action.response.Preflight.BlockedReasons),
		blockingReasons:   append([]types.ControlBlockedReason{}, action.response.Preflight.BlockedReasons...),
		readinessState:    snapshot.readinessState,
		recommendedAction: snapshot.recommendedAction,
		actionEvaluation:  action,
	}
	return plan
}

func applyExecutionRevalidation(plan controlExecutionPlan, revalidated controlExecutionRevalidation) controlExecutionPlan {
	plan.executionMode = revalidated.executionMode
	plan.placeholderOnly = false
	plan.targetFacts = cloneFacts(revalidated.targetFacts)
	plan.preflight = clonePreflightSummary(revalidated.preflight)
	plan.primaryReasonCode = revalidated.primaryReason
	plan.blockingReasons = append([]types.ControlBlockedReason{}, revalidated.blockingReasons...)
	plan.recommendedAction = revalidated.recommendedAction
	plan.readinessState = revalidated.readinessState
	updated := cloneControlActionResponse(plan.actionEvaluation.response)
	updated.Result = types.ControlResultBlocked
	updated.HumanMessage = revalidated.humanMessage
	updated.Facts = cloneFacts(revalidated.targetFacts)
	updated.Preflight = clonePreflightSummary(revalidated.preflight)
	updated.ExecutionMode = revalidated.executionMode
	updated.PlaceholderOnly = false
	updated.ExecutionNotes = cloneExecutionNotes(revalidated.executionNotes)
	plan.actionEvaluation.response = updated
	return plan
}

func effectiveExecutionModeForPlan(plan controlExecutionPlan) types.ControlExecutionMode {
	if plan.actionEvaluation.execute.executionMode != "" {
		return plan.actionEvaluation.execute.executionMode
	}
	if plan.executionMode != "" {
		return plan.executionMode
	}
	return types.ControlExecutionPlaceholder
}

func inferRecommendedActionFromPreflight(targetKind types.ControlTargetKind, actionKind types.ControlActionKind, preflight types.ControlPreflightSummary, facts map[string]string) types.ControlActionKind {
	if targetKind == types.ControlTargetNode {
		if strings.TrimSpace(facts["isolated"]) == "true" {
			return types.ControlActionReleaseNode
		}
		if actionKind == types.ControlActionReleaseNode {
			return types.ControlActionIsolateNode
		}
		return actionKind
	}
	if targetKind == types.ControlTargetTunnel {
		if strings.TrimSpace(facts["tunnelStatus"]) == "paused" {
			return types.ControlActionResumeTunnel
		}
		return types.ControlActionPauseTunnel
	}
	if len(preflight.BlockedReasons) > 0 {
		return actionKind
	}
	return actionKind
}

func revalidateRequestedControlContext(plan controlExecutionPlan) controlExecutionRevalidation {
	currentVersion := strings.TrimSpace(plan.targetFacts["controlStateUpdatedAt"])
	requestedVersion := requestedControlContextVersion(plan)
	if currentVersion == "" || requestedVersion == "" || currentVersion == requestedVersion {
		return controlExecutionRevalidation{allowed: true}
	}
	if currentTs, currentOk := parseControlStateTimestamp(currentVersion); currentOk {
		if requestedTs, requestedOk := parseControlStateTimestamp(requestedVersion); requestedOk && !currentTs.After(requestedTs.UTC()) {
			return controlExecutionRevalidation{allowed: true}
		}
	}
	message := "目标状态已变化，请刷新后重试。"
	if len(plan.preflight.BlockedReasons) > 0 {
		message = "目标状态已变化，请刷新后重试。当前原因: " + plan.preflight.BlockedReasons[0].Message
	}
	return controlExecutionRevalidation{
		allowed:       false,
		outcome:       controlExecutionOutcomePolicyRejected,
		humanMessage:  message,
		executionMode: effectiveExecutionModeForPlan(plan),
		executionNotes: []types.ControlExecutionNote{{
			Message: "执行前发现控制上下文已经过期，目标状态已变化，请刷新控制摘要后重试。",
		}},
		blockingReasons:   append([]types.ControlBlockedReason{}, plan.preflight.BlockedReasons...),
		primaryReason:     plan.primaryReasonCode,
		targetFacts:       cloneFacts(plan.targetFacts),
		preflight:         clonePreflightSummary(plan.preflight),
		recommendedAction: plan.recommendedAction,
		readinessState:    plan.readinessState,
	}
}

func requestedControlContextVersion(plan controlExecutionPlan) string {
	if !plan.requestedAt.IsZero() {
		return plan.requestedAt.UTC().Format(time.RFC3339Nano)
	}
	if raw := strings.TrimSpace(plan.note); strings.HasPrefix(raw, "contextVersion=") {
		return strings.TrimPrefix(raw, "contextVersion=")
	}
	return ""
}

func parseControlStateTimestamp(raw string) (time.Time, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, trimmed)
		if err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func controlContextVersionFromFacts(facts map[string]string) string {
	if len(facts) == 0 {
		return ""
	}
	return strings.TrimSpace(facts["controlStateUpdatedAt"])
}

func snapshotContextVersion(actions []evaluatedControlAction) string {
	var newest time.Time
	var version string
	for _, action := range actions {
		candidate := controlContextVersionFromFacts(action.response.Facts)
		parsed, ok := parseControlStateTimestamp(candidate)
		if !ok {
			continue
		}
		if newest.IsZero() || parsed.After(newest) {
			newest = parsed
			version = candidate
		}
	}
	return version
}

func readinessStateFromPreflight(preflight types.ControlPreflightSummary) types.ControlReadinessState {
	if preflight.Allowed {
		return types.ControlReadinessReady
	}
	if containsMissingCheck(preflight.Items) {
		return types.ControlReadinessPartial
	}
	return types.ControlReadinessBlocked
}

func buildControlActionResponse(req types.ControlActionRequest, plan controlExecutionPlan, result controlExecutionResult) types.ControlActionResponse {
	resp := cloneControlActionResponse(plan.actionEvaluation.response)
	resp.ActionKind = req.ActionKind
	resp.TargetKind = req.TargetKind
	resp.TargetID = req.TargetID
	resp.SourceSurface = req.SourceSurface
	resp.DryRunOnly = false
	resp.Facts = cloneFacts(plan.targetFacts)
	resp.Preflight = clonePreflightSummary(plan.preflight)
	resp.ExecutionMode = result.executionMode
	if resp.ExecutionMode == "" {
		resp.ExecutionMode = plan.executionMode
	}
	switch result.outcome {
	case controlExecutionOutcomeAcceptedReal:
		resp.Result = types.ControlResultAccepted
		resp.HumanMessage = defaultControlMessage(result.humanMessage, "已通过真实执行器触发 agent restart。")
		resp.PlaceholderOnly = false
		resp.ExecutionNotes = defaultExecutionNotes(result.executionNotes, []types.ControlExecutionNote{{Message: "已通过真实执行器提交 restart 请求。"}})
	case controlExecutionOutcomeAcceptedPlaceholder:
		resp.Result = types.ControlResultAccepted
		resp.HumanMessage = defaultControlMessage(result.humanMessage, "动作已受理，但当前只接入 placeholder execution boundary，未执行真实系统动作。")
		resp.PlaceholderOnly = true
		resp.ExecutionNotes = defaultExecutionNotes(result.executionNotes, placeholderExecutionNotes())
	case controlExecutionOutcomeBlockedPreflight:
		resp.Result = types.ControlResultBlocked
		resp.HumanMessage = defaultControlMessage(result.humanMessage, "execute 已被预检阻断。")
		resp.PlaceholderOnly = false
		resp.ExecutionNotes = cloneExecutionNotes(result.executionNotes)
	case controlExecutionOutcomePolicyRejected:
		resp.Result = types.ControlResultRejected
		resp.HumanMessage = defaultControlMessage(result.humanMessage, "执行策略拒绝了当前动作。")
		resp.PlaceholderOnly = false
		resp.ExecutionNotes = defaultExecutionNotes(result.executionNotes, []types.ControlExecutionNote{{Message: "执行策略拒绝执行当前动作。"}})
	case controlExecutionOutcomeRetryableFailure:
		resp.Result = types.ControlResultRejected
		resp.HumanMessage = defaultControlMessage(result.humanMessage, "执行器处理当前动作失败，但可稍后重试。")
		resp.PlaceholderOnly = false
		resp.ExecutionNotes = defaultExecutionNotes(result.executionNotes, []types.ControlExecutionNote{{Message: "执行器处理失败，建议稍后重试。"}})
	case controlExecutionOutcomeNonRetryableFailure:
		resp.Result = types.ControlResultRejected
		resp.HumanMessage = defaultControlMessage(result.humanMessage, "执行器处理当前动作失败，当前不建议重试。")
		resp.PlaceholderOnly = false
		resp.ExecutionNotes = defaultExecutionNotes(result.executionNotes, []types.ControlExecutionNote{{Message: "执行器处理失败，当前不建议重试。"}})
	default:
		resp.Result = types.ControlResultRejected
		resp.HumanMessage = defaultControlMessage(result.humanMessage, "执行器返回了未知结果。")
		resp.PlaceholderOnly = false
		resp.ExecutionNotes = defaultExecutionNotes(result.executionNotes, []types.ControlExecutionNote{{Message: "执行器返回了未知结果。"}})
	}
	return resp
}

func acceptedRealExecutionResult(plan controlExecutionPlan, serviceUnit string) controlExecutionResult {
	return controlExecutionResult{
		outcome:       controlExecutionOutcomeAcceptedReal,
		humanMessage:  "已通过真实执行器触发 agent restart。",
		executionMode: types.ControlExecutionReal,
		executionNotes: []types.ControlExecutionNote{{
			Message: "systemctl restart " + serviceUnit + " 已提交。",
		}},
	}
}

func acceptedStateMutationExecutionResult(message string, note string) controlExecutionResult {
	return controlExecutionResult{
		outcome:       controlExecutionOutcomeAcceptedReal,
		humanMessage:  message,
		executionMode: types.ControlExecutionReal,
		executionNotes: []types.ControlExecutionNote{{
			Message: note,
		}},
	}
}

func acceptedPlaceholderExecutionResult(plan controlExecutionPlan) controlExecutionResult {
	return controlExecutionResult{
		outcome:       controlExecutionOutcomeAcceptedPlaceholder,
		humanMessage:  "动作已受理，但当前只接入 placeholder execution boundary，未执行真实系统动作。",
		executionMode: plan.executionMode,
		executionNotes: []types.ControlExecutionNote{{
			Code:    types.ControlReasonPlaceholderOnly,
			Message: "当前还没有接入真实系统执行器。",
		}},
	}
}

func blockedExecutionResult(plan controlExecutionPlan) controlExecutionResult {
	return controlExecutionResult{
		outcome:        controlExecutionOutcomeBlockedPreflight,
		humanMessage:   "execute 已被预检阻断。",
		executionMode:  plan.executionMode,
		executionNotes: []types.ControlExecutionNote{},
	}
}

func policyRejectedExecutionResult(plan controlExecutionPlan, message string) controlExecutionResult {
	return controlExecutionResult{
		outcome:       controlExecutionOutcomePolicyRejected,
		humanMessage:  message,
		executionMode: types.ControlExecutionReal,
		executionNotes: []types.ControlExecutionNote{{
			Message: message,
		}},
	}
}

func retryableFailureExecutionResult(plan controlExecutionPlan, message string) controlExecutionResult {
	return controlExecutionResult{
		outcome:       controlExecutionOutcomeRetryableFailure,
		humanMessage:  message,
		executionMode: types.ControlExecutionReal,
		executionNotes: []types.ControlExecutionNote{{
			Message: message,
		}},
	}
}

func nonRetryableFailureExecutionResult(plan controlExecutionPlan, message string) controlExecutionResult {
	return controlExecutionResult{
		outcome:       controlExecutionOutcomeNonRetryableFailure,
		humanMessage:  message,
		executionMode: types.ControlExecutionReal,
		executionNotes: []types.ControlExecutionNote{{
			Message: message,
		}},
	}
}

func validateRestartAgentExecutionPolicy(plan controlExecutionPlan, allowedServicePrefix string, serviceUnit string) (controlExecutionResult, bool) {
	if strings.TrimSpace(plan.targetFacts["deploymentMode"]) != "managed" {
		return policyRejectedExecutionResult(plan, "真实 restart 只允许 deploymentMode=managed 的节点。"), true
	}
	if strings.TrimSpace(plan.targetFacts["instanceManaged"]) != "true" {
		return policyRejectedExecutionResult(plan, "真实 restart 只允许 instanceManaged=true 的节点。"), true
	}
	if strings.TrimSpace(serviceUnit) == "" {
		return policyRejectedExecutionResult(plan, "真实 restart 需要已上报 serviceUnit。"), true
	}
	if !strings.HasSuffix(serviceUnit, ".service") || !strings.HasPrefix(serviceUnit, allowedServicePrefix) {
		return policyRejectedExecutionResult(plan, "真实 restart 只允许安全前缀内的 systemd serviceUnit。"), true
	}
	if strings.TrimSpace(plan.targetFacts["instanceProfile"]) == "" {
		return policyRejectedExecutionResult(plan, "真实 restart 需要已上报 instanceProfile。"), true
	}
	return controlExecutionResult{}, false
}

func classifyRestartAgentCommandResult(plan controlExecutionPlan, serviceUnit string, result controlCommandResult) controlExecutionResult {
	if result.err == nil && result.exitCode == 0 {
		return acceptedRealExecutionResult(plan, serviceUnit)
	}
	detail := restartCommandDetail(result)
	lowerDetail := strings.ToLower(detail)
	if errors.Is(result.err, context.DeadlineExceeded) || errors.Is(result.err, context.Canceled) {
		return retryableFailureExecutionResult(plan, "真实 restart 调用超时或被取消，可稍后重试。详情: "+detail)
	}
	if errors.Is(result.err, exec.ErrNotFound) || result.exitCode == 5 || strings.Contains(lowerDetail, "not found") || strings.Contains(lowerDetail, "not loaded") || strings.Contains(lowerDetail, "permission denied") || strings.Contains(lowerDetail, "access denied") || strings.Contains(lowerDetail, "authentication is required") {
		return nonRetryableFailureExecutionResult(plan, "真实 restart 调用失败，当前不建议重试。详情: "+detail)
	}
	return retryableFailureExecutionResult(plan, "真实 restart 调用失败，可稍后重试。详情: "+detail)
}

func restartCommandDetail(result controlCommandResult) string {
	for _, candidate := range []string{result.stderr, result.stdout} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	if result.err != nil {
		return result.err.Error()
	}
	return "no command detail"
}

func (s *Server) writeControlExecutionAudit(ctx context.Context, req types.ControlActionRequest, plan controlExecutionPlan, result controlExecutionResult, resp types.ControlActionResponse) {
	if result.outcome == controlExecutionOutcomeBlockedPreflight || req.DryRun {
		return
	}
	resourceType := string(req.TargetKind)
	if resourceType == "" {
		resourceType = "control_target"
	}
	action := "control_execute_rejected"
	switch result.outcome {
	case controlExecutionOutcomeAcceptedReal:
		action = "control_execute_accepted"
	case controlExecutionOutcomeAcceptedPlaceholder:
		action = "control_execute_placeholder_accepted"
	case controlExecutionOutcomePolicyRejected:
		action = "control_execute_policy_rejected"
	case controlExecutionOutcomeRetryableFailure:
		action = "control_execute_retryable_failure"
	case controlExecutionOutcomeNonRetryableFailure:
		action = "control_execute_non_retryable_failure"
	}
	actorType, actorID := s.currentActorFromContext(ctx)
	payload := map[string]string{
		"actionKind":        string(req.ActionKind),
		"sourceSurface":     string(req.SourceSurface),
		"executionMode":     string(resp.ExecutionMode),
		"placeholderOnly":   strconv.FormatBool(resp.PlaceholderOnly),
		"result":            string(resp.Result),
		"outcome":           string(result.outcome),
		"note":              strings.TrimSpace(req.Note),
		"recommendedAction": string(plan.recommendedAction),
		"readinessState":    string(plan.readinessState),
		"targetStateBefore": controlAuditBeforeState(req, plan, result),
		"targetStateAfter":  controlAuditAfterState(req, resp),
		"primaryReasonCode": string(plan.primaryReasonCode),
		"humanMessage":      resp.HumanMessage,
	}
	if strings.TrimSpace(result.auditHint) != "" {
		payload["rejectionKind"] = result.auditHint
	}
	_, _ = s.store.WriteAuditLog(ctx, store.AuditLogParams{
		ActorType:    actorType,
		ActorID:      actorID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   req.TargetID,
		Payload:      payload,
	})
}

func (s *Server) currentActorFromContext(ctx context.Context) (string, string) {
	if user, ok := authUserFromContext(ctx); ok {
		return "user", user.ID
	}
	return "system", ""
}

func controlAuditBeforeState(req types.ControlActionRequest, plan controlExecutionPlan, result controlExecutionResult) string {
	if result.auditHint == "state_drift" {
		if inferred := inferredDriftBeforeState(req.ActionKind, req.TargetKind); inferred != "" {
			return inferred
		}
	}
	facts := plan.previewFacts
	if len(facts) == 0 {
		facts = plan.actionEvaluation.response.Facts
	}
	if len(facts) == 0 {
		facts = plan.targetFacts
	}
	if req.TargetKind == types.ControlTargetNode {
		return "isolated=" + facts["isolated"]
	}
	if req.TargetKind == types.ControlTargetTunnel {
		return "status=" + facts["tunnelStatus"]
	}
	return ""
}

func inferredDriftBeforeState(actionKind types.ControlActionKind, targetKind types.ControlTargetKind) string {
	if targetKind == types.ControlTargetNode {
		switch actionKind {
		case types.ControlActionIsolateNode, types.ControlActionRestartAgent:
			return "isolated=false"
		case types.ControlActionReleaseNode:
			return "isolated=true"
		default:
			return ""
		}
	}
	if targetKind == types.ControlTargetTunnel {
		switch actionKind {
		case types.ControlActionPauseTunnel:
			return "status=active"
		case types.ControlActionResumeTunnel:
			return "status=paused"
		default:
			return ""
		}
	}
	return ""
}

func controlAuditAfterState(req types.ControlActionRequest, resp types.ControlActionResponse) string {
	if resp.Result != types.ControlResultAccepted {
		if req.TargetKind == types.ControlTargetNode {
			return "isolated=" + resp.Facts["isolated"]
		}
		if req.TargetKind == types.ControlTargetTunnel {
			return "status=" + resp.Facts["tunnelStatus"]
		}
		return ""
	}
	if req.TargetKind == types.ControlTargetNode {
		switch req.ActionKind {
		case types.ControlActionIsolateNode:
			return "isolated=true"
		case types.ControlActionReleaseNode:
			return "isolated=false"
		default:
			return "isolated=" + resp.Facts["isolated"]
		}
	}
	if req.TargetKind == types.ControlTargetTunnel {
		switch req.ActionKind {
		case types.ControlActionPauseTunnel:
			return "status=paused"
		case types.ControlActionResumeTunnel:
			return "status=active"
		default:
			return "status=" + resp.Facts["tunnelStatus"]
		}
	}
	return ""
}

func placeholderExecutionNotes() []types.ControlExecutionNote {
	return []types.ControlExecutionNote{{
		Code:    types.ControlReasonPlaceholderOnly,
		Message: "当前还没有接入真实系统执行器。",
	}}
}

func defaultExecutionNotes(notes []types.ControlExecutionNote, fallback []types.ControlExecutionNote) []types.ControlExecutionNote {
	if len(notes) > 0 {
		return cloneExecutionNotes(notes)
	}
	return cloneExecutionNotes(fallback)
}

func cloneExecutionNotes(notes []types.ControlExecutionNote) []types.ControlExecutionNote {
	return append([]types.ControlExecutionNote{}, notes...)
}

func defaultControlMessage(message string, fallback string) string {
	if strings.TrimSpace(message) != "" {
		return message
	}
	return fallback
}

func (s *Server) buildControlActionOptions(ctx context.Context, targetKind types.ControlTargetKind, targetID string, surface types.ControlSurface) (types.ControlActionOptionsResponse, int) {
	snapshot, status := s.buildControlTargetSnapshot(ctx, targetKind, targetID, surface)
	if status != http.StatusOK {
		return types.ControlActionOptionsResponse{}, status
	}
	resp := types.ControlActionOptionsResponse{
		TargetKind:     targetKind,
		TargetID:       targetID,
		SourceSurface:  surface,
		ContextVersion: snapshot.contextVersion,
		ExecutionMode:  snapshot.executionMode,
		Items:          []types.ControlActionOption{},
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
		ContextVersion:    snapshot.contextVersion,
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
		req := types.ControlActionRequest{
			ActionKind:    actionKind,
			TargetKind:    targetKind,
			TargetID:      targetID,
			SourceSurface: surface,
			DryRun:        true,
			RequestedAt:   time.Now().UTC(),
		}
		resp, status := s.evaluateControlActionDryRun(ctx, req)
		if status != http.StatusOK {
			return snapshot, status
		}
		evaluated := evaluatedControlAction{
			request:  req,
			response: resp,
			status:   status,
		}
		if resp.Preflight.Allowed {
			plan := buildExecutionPlan(types.ControlActionRequest{
				ActionKind:    actionKind,
				TargetKind:    targetKind,
				TargetID:      targetID,
				SourceSurface: surface,
				RequestedAt:   req.RequestedAt,
			}, snapshot, evaluated)
			evaluated.execute = s.previewExecutionOutcome(ctx, plan)
		} else {
			plan := buildExecutionPlan(types.ControlActionRequest{
				ActionKind:    actionKind,
				TargetKind:    targetKind,
				TargetID:      targetID,
				SourceSurface: surface,
				RequestedAt:   req.RequestedAt,
			}, snapshot, evaluated)
			evaluated.execute = blockedExecutionResult(plan)
		}
		snapshot.actions = append(snapshot.actions, evaluatedControlAction{
			request:  evaluated.request,
			response: evaluated.response,
			execute:  evaluated.execute,
			status:   evaluated.status,
		})
	}
	snapshot.contextVersion = snapshotContextVersion(snapshot.actions)
	snapshot.executionMode, snapshot.placeholderOnly = snapshotExecutionSemantics(snapshot.actions, snapshot.recommendedAction)
	snapshot.recommendedAction = recommendedActionForSnapshot(targetKind, snapshot.actions)
	snapshot.executionMode, snapshot.placeholderOnly = snapshotExecutionSemantics(snapshot.actions, snapshot.recommendedAction)
	snapshot.checks, snapshot.blockedReasons = aggregateTargetChecks(targetKind, snapshot.actions, snapshot.recommendedAction)
	snapshot.primaryReasonCode = firstPrimaryReason(snapshot.blockedReasons)
	snapshot.headline, snapshot.summary, snapshot.nextStep, snapshot.readinessState = summarizeSnapshot(snapshot)
	return snapshot, http.StatusOK
}

func (s *Server) previewExecutionOutcome(ctx context.Context, plan controlExecutionPlan) controlExecutionResult {
	if preview, ok := s.previewRealExecutionOutcome(ctx, plan); ok {
		return preview
	}
	return acceptedPlaceholderExecutionResult(plan)
}

func (s *Server) previewRealExecutionOutcome(_ context.Context, plan controlExecutionPlan) (controlExecutionResult, bool) {
	if _, ok := unwrapStateMutationControlExecutor(s.controlExecutor); ok {
		switch plan.actionKind {
		case types.ControlActionIsolateNode:
			return acceptedStateMutationExecutionResult("当前动作会真实更新节点隔离状态。", "节点将被更新为 isolated=true。"), true
		case types.ControlActionReleaseNode:
			return acceptedStateMutationExecutionResult("当前动作会真实更新节点隔离状态。", "节点将被更新为 isolated=false。"), true
		case types.ControlActionPauseTunnel:
			return acceptedStateMutationExecutionResult("当前动作会真实更新 tunnel 状态。", "tunnel status 将被更新为 paused。"), true
		case types.ControlActionResumeTunnel:
			return acceptedStateMutationExecutionResult("当前动作会真实更新 tunnel 状态。", "tunnel status 将被更新为 active。"), true
		}
	}
	restartExecutor, ok := unwrapRestartAgentExecutor(s.controlExecutor)
	if !ok {
		return controlExecutionResult{}, false
	}
	if plan.actionKind != types.ControlActionRestartAgent || plan.targetKind != types.ControlTargetNode {
		return controlExecutionResult{}, false
	}
	if !restartExecutor.config.enabled {
		return controlExecutionResult{}, false
	}
	if plan.sourceSurface != types.ControlSurfaceNodeConsole {
		return controlExecutionResult{}, false
	}
	if strings.TrimSpace(restartExecutor.config.localNodeID) == "" || plan.targetID != restartExecutor.config.localNodeID {
		return controlExecutionResult{}, false
	}
	serviceUnit := strings.TrimSpace(plan.targetFacts["serviceUnit"])
	if rejected, blocked := validateRestartAgentExecutionPolicy(plan, restartExecutor.config.allowedServicePrefix, serviceUnit); blocked {
		return rejected, true
	}
	return acceptedRealExecutionResult(plan, serviceUnit), true
}

func unwrapRestartAgentExecutor(executor controlExecutor) (restartAgentExecutor, bool) {
	switch value := executor.(type) {
	case stateMutationControlExecutor:
		return unwrapRestartAgentExecutor(value.fallback)
	case *stateMutationControlExecutor:
		return unwrapRestartAgentExecutor(value.fallback)
	case restartCapableControlExecutor:
		return value.restart, true
	case *restartCapableControlExecutor:
		return value.restart, true
	default:
		return restartAgentExecutor{}, false
	}
}

func unwrapStateMutationControlExecutor(executor controlExecutor) (stateMutationControlExecutor, bool) {
	switch value := executor.(type) {
	case stateMutationControlExecutor:
		return value, true
	case *stateMutationControlExecutor:
		return *value, true
	default:
		return stateMutationControlExecutor{}, false
	}
}

func snapshotExecutionSemantics(actions []evaluatedControlAction, recommendedAction types.ControlActionKind) (types.ControlExecutionMode, bool) {
	recommended := findEvaluatedAction(actions, recommendedAction)
	if recommended != nil {
		if recommended.execute.executionMode == types.ControlExecutionReal {
			return types.ControlExecutionReal, false
		}
		if recommended.execute.outcome == controlExecutionOutcomeAcceptedPlaceholder {
			return types.ControlExecutionPlaceholder, true
		}
	}
	for _, action := range actions {
		if action.execute.executionMode == types.ControlExecutionReal && action.execute.outcome == controlExecutionOutcomeAcceptedReal {
			return types.ControlExecutionReal, false
		}
	}
	return types.ControlExecutionPlaceholder, true
}

func summarizeSnapshot(snapshot controlTargetSnapshot) (string, string, string, types.ControlReadinessState) {
	if len(snapshot.checks) == 0 {
		return "当前控制前提已阻断。", "当前没有可用的控制检查。", "请重新读取控制摘要。", types.ControlReadinessBlocked
	}
	if snapshot.primaryReasonCode == "" {
		if snapshot.executionMode == types.ControlExecutionReal && !snapshot.placeholderOnly {
			return "当前控制前提已满足。", "当前主要控制前提已经满足，推荐动作可以进入真实执行路径。", "可先执行 dry-run，再按需触发真实执行。", types.ControlReadinessReady
		}
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
			if actionExecutesReal(actions, types.ControlActionReleaseNode) {
				return types.ControlActionReleaseNode
			}
			if hasAction(actions, types.ControlActionReleaseNode) {
				return types.ControlActionReleaseNode
			}
		}
		if actionExecutesReal(actions, types.ControlActionRestartAgent) {
			return types.ControlActionRestartAgent
		}
		if actionExecutesReal(actions, types.ControlActionIsolateNode) {
			return types.ControlActionIsolateNode
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
		if actionExecutesReal(actions, types.ControlActionResumeTunnel) {
			return types.ControlActionResumeTunnel
		}
		if actionExecutesReal(actions, types.ControlActionPauseTunnel) {
			return types.ControlActionPauseTunnel
		}
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

func actionExecutesReal(actions []evaluatedControlAction, actionKind types.ControlActionKind) bool {
	for _, action := range actions {
		if action.request.ActionKind == actionKind && action.execute.outcome == controlExecutionOutcomeAcceptedReal {
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

func findActionSnapshot(snapshot controlTargetSnapshot, actionKind types.ControlActionKind) *evaluatedControlAction {
	return findEvaluatedAction(snapshot.actions, actionKind)
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
	contextVersion := controlContextVersionFromFacts(result.Facts)
	if result.Result == types.ControlResultAccepted {
		summary, nextStep := controlOptionSummaryAndNextStep(result, nil, evaluated.execute)
		availabilityState := types.ControlAvailabilityPlaceholderOnly
		placeholderOnly := true
		executionMode := types.ControlExecutionPlaceholder
		executionNotes := []types.ControlExecutionNote{{Code: types.ControlReasonPlaceholderOnly, Message: "当前只会进入 placeholder execute。"}}
		if evaluated.execute.outcome == controlExecutionOutcomeAcceptedReal {
			availabilityState = types.ControlAvailabilityAvailable
			placeholderOnly = false
			executionMode = types.ControlExecutionReal
			executionNotes = cloneExecutionNotes(evaluated.execute.executionNotes)
		}
		return types.ControlActionOption{
			ActionKind:        result.ActionKind,
			TargetKind:        result.TargetKind,
			TargetID:          result.TargetID,
			SourceSurface:     result.SourceSurface,
			ContextVersion:    contextVersion,
			Available:         true,
			AvailabilityState: availabilityState,
			Label:             controlActionLabel(result.ActionKind),
			Message:           result.HumanMessage,
			Summary:           summary,
			NextStep:          nextStep,
			ExecutionMode:     executionMode,
			PlaceholderOnly:   placeholderOnly,
			ExecutionNotes:    executionNotes,
		}
	}
	var primaryReason types.ControlReasonCode
	if len(result.Preflight.BlockedReasons) > 0 {
		primaryReason = result.Preflight.BlockedReasons[0].Code
	}
	summary, nextStep := controlOptionSummaryAndNextStep(result, result.Preflight.BlockedReasons, evaluated.execute)
	return types.ControlActionOption{
		ActionKind:        result.ActionKind,
		TargetKind:        result.TargetKind,
		TargetID:          result.TargetID,
		SourceSurface:     result.SourceSurface,
		ContextVersion:    contextVersion,
		Available:         false,
		AvailabilityState: types.ControlAvailabilityBlocked,
		Label:             controlActionLabel(result.ActionKind),
		Message:           result.HumanMessage,
		Summary:           summary,
		NextStep:          nextStep,
		PrimaryReasonCode: primaryReason,
		ExecutionMode:     evaluated.execute.executionMode,
		PlaceholderOnly:   false,
		ReasonHints:       append([]types.ControlBlockedReason{}, result.Preflight.BlockedReasons...),
	}
}

func controlOptionSummaryAndNextStep(result types.ControlActionResponse, reasons []types.ControlBlockedReason, execution controlExecutionResult) (string, string) {
	if result.Result == types.ControlResultAccepted {
		if execution.outcome == controlExecutionOutcomeAcceptedReal {
			return "当前动作可以发起，并会进入真实执行路径。", "建议先执行 dry-run，再确认服务状态与节点上下文。"
		}
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

func populateNodeControlFacts(resp *types.ControlActionResponse, node types.NodeSummary) {
	resp.Facts["nodeStatus"] = node.Status
	resp.Facts["deploymentMode"] = node.DeploymentMode
	resp.Facts["serviceUnit"] = node.ServiceUnit
	resp.Facts["instanceProfile"] = node.InstanceProfile
	resp.Facts["instanceManaged"] = strconv.FormatBool(node.InstanceManaged)
	resp.Facts["isolated"] = strconv.FormatBool(node.Isolated)
	if value := strings.TrimSpace(node.Metadata["controlStateUpdatedAt"]); value != "" {
		resp.Facts["controlStateUpdatedAt"] = value
	}
}

func populateTunnelControlFacts(resp *types.ControlActionResponse, tunnel types.TunnelSpec) {
	resp.Facts["tunnelStatus"] = tunnel.Status
	resp.Facts["runtimeState"] = tunnel.RuntimeState
	resp.Facts["runtimePath"] = tunnel.RuntimePath
	resp.Facts["transportPolicy"] = tunnel.TransportPolicy
	if !tunnel.UpdatedAt.IsZero() {
		resp.Facts["controlStateUpdatedAt"] = tunnel.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
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
