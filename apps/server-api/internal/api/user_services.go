package api

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/25743/cloud-relay-platform/apps/server-api/internal/store"
	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

const (
	serviceMetaKeyKey           = "serviceKey"
	serviceMetaTitleKey         = "serviceTitle"
	serviceMetaKindKey          = "serviceKind"
	serviceMetaSummaryKey       = "serviceSummary"
	serviceMetaPublicURLKey     = "servicePublicUrl"
	serviceMetaP2PURLKey        = "serviceP2PUrl"
	serviceMetaP2PNodeIDKey     = "serviceP2PNodeId"
	serviceMetaP2PPortKey       = "serviceP2PTargetPort"
	serviceMetaP2PPathKey       = "serviceP2PPath"
	serviceMetaCloudAccessKey   = "serviceCloudAccess"
	serviceMetaP2PAccessKey     = "serviceP2PAccess"
	serviceMetaPreferredPathKey = "servicePreferredPath"

	serviceAccessAllUsers  = "all_users"
	serviceAccessAdminOnly = "admin_only"
	serviceAccessDisabled  = "disabled"

	serviceRegistrationSourceMetadata = "metadata"
	serviceRegistrationSourceInferred = "inferred"
)

func (s *Server) handleUserServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	user, ok := authUserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	tunnels, err := s.store.ListTunnels(r.Context(), store.TunnelFilter{Status: "active"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tunnels = s.withTunnelHealth(r.Context(), tunnels)

	nodes, _, err := s.store.ListNodes(r.Context(), store.NodeFilter{Limit: 100000, Offset: 0})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nodeIndex := make(map[string]types.NodeSummary, len(nodes))
	for _, node := range nodes {
		nodeIndex[node.NodeID] = node
	}

	services := make([]types.UserServiceEntry, 0, len(tunnels))
	for _, tunnel := range tunnels {
		entry, ok := userServiceEntryFromTunnel(r, user, tunnel, nodeIndex)
		if !ok {
			continue
		}
		services = append(services, entry)
	}

	sort.Slice(services, func(i, j int) bool {
		if services[i].Key != services[j].Key {
			return services[i].Key < services[j].Key
		}
		if services[i].Title != services[j].Title {
			return services[i].Title < services[j].Title
		}
		return services[i].TunnelID < services[j].TunnelID
	})

	writeJSON(w, http.StatusOK, types.UserServiceCatalogResponse{Items: services})
}

func (s *Server) handlePublicServiceTransport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	normalizedPublicURL := normalizeServiceURLValue(r.URL.Query().Get("publicUrl"))
	if normalizedPublicURL == "" {
		writeError(w, http.StatusBadRequest, "publicUrl is required")
		return
	}

	kind := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("kind")))
	if kind == "" {
		kind = "music"
	}

	tunnels, err := s.store.ListTunnels(r.Context(), store.TunnelFilter{Status: "active"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	nodes, _, err := s.store.ListNodes(r.Context(), store.NodeFilter{Limit: 100000, Offset: 0})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nodeIndex := make(map[string]types.NodeSummary, len(nodes))
	for _, node := range nodes {
		nodeIndex[node.NodeID] = node
	}

	entry, ok := publicServiceTransportFromTunnels(r, tunnels, nodeIndex, kind, normalizedPublicURL)
	if !ok {
		writeError(w, http.StatusNotFound, "service transport not found")
		return
	}

	writeJSON(w, http.StatusOK, entry)
}

func userServiceEntryFromTunnel(
	r *http.Request,
	user types.UserSummary,
	tunnel types.TunnelSpec,
	nodeIndex map[string]types.NodeSummary,
) (types.UserServiceEntry, bool) {
	key, title, kind, summary, registrationSource, ok := resolveServiceIdentity(tunnel)
	if !ok {
		return types.UserServiceEntry{}, false
	}

	serviceNode := resolveServiceNode(tunnel, nodeIndex)
	publicURL := strings.TrimSpace(tunnel.Metadata[serviceMetaPublicURLKey])
	if publicURL == "" {
		publicURL = deriveServicePublicURL(r, tunnel)
	}
	p2pURL := strings.TrimSpace(tunnel.Metadata[serviceMetaP2PURLKey])
	if p2pURL == "" {
		p2pURL = deriveServiceP2PURL(tunnel, serviceNode)
	}

	cloudAccess := normalizeServiceAccessPolicy(tunnel.Metadata[serviceMetaCloudAccessKey], publicURL != "")
	p2pAccess := normalizeServiceAccessPolicy(tunnel.Metadata[serviceMetaP2PAccessKey], p2pURL != "")
	p2pAccess = normalizeEndUserP2PAccess(key, kind, p2pAccess, p2pURL != "")
	preferredPath := normalizeServicePreferredPath(
		tunnel.Metadata[serviceMetaPreferredPathKey],
		kind,
		publicURL != "",
		p2pURL != "",
	)

	return types.UserServiceEntry{
		Key:                key,
		Title:              title,
		Kind:               kind,
		Summary:            summary,
		RegistrationSource: registrationSource,
		NodeID:             serviceNode.NodeID,
		NodeName:           serviceNode.NodeName,
		NodeStatus:         serviceNode.Status,
		TunnelID:           tunnel.ID,
		TunnelName:         tunnel.Name,
		TunnelType:         tunnel.Type,
		TunnelStatus:       tunnel.Status,
		TransportPolicy:    tunnel.TransportPolicy,
		RuntimePath:        tunnel.RuntimePath,
		RuntimeState:       tunnel.RuntimeState,
		HealthStatus:       string(tunnel.HealthStatus),
		PublicURL:          publicURL,
		P2PURL:             p2pURL,
		CloudAccess:        cloudAccess,
		P2PAccess:          p2pAccess,
		CloudAllowed:       serviceAccessAllowed(cloudAccess, user.Role),
		P2PAllowed:         serviceAccessAllowed(p2pAccess, user.Role),
		PreferredPath:      preferredPath,
		TransportManifest:  buildServiceTransportManifest(kind, publicURL, p2pURL, preferredPath),
	}, true
}

func publicServiceTransportFromTunnels(
	r *http.Request,
	tunnels []types.TunnelSpec,
	nodeIndex map[string]types.NodeSummary,
	kind string,
	normalizedPublicURL string,
) (types.PublicServiceTransportResponse, bool) {
	normalizedKind := strings.TrimSpace(strings.ToLower(kind))
	for _, tunnel := range tunnels {
		key, _, inferredKind, _, _, ok := resolveServiceIdentity(tunnel)
		if !ok {
			continue
		}
		if strings.TrimSpace(strings.ToLower(inferredKind)) != normalizedKind {
			continue
		}

		publicURL := strings.TrimSpace(tunnel.Metadata[serviceMetaPublicURLKey])
		if publicURL == "" {
			publicURL = deriveServicePublicURL(r, tunnel)
		}
		if normalizeServiceURLValue(publicURL) != normalizedPublicURL {
			continue
		}

		entry, ok := publicServiceTransportFromTunnel(r, tunnel, nodeIndex, normalizedKind)
		if !ok {
			continue
		}
		entry.Key = key
		return entry, true
	}
	return types.PublicServiceTransportResponse{}, false
}

func publicServiceTransportFromTunnel(
	r *http.Request,
	tunnel types.TunnelSpec,
	nodeIndex map[string]types.NodeSummary,
	kind string,
) (types.PublicServiceTransportResponse, bool) {
	key, _, inferredKind, _, _, ok := resolveServiceIdentity(tunnel)
	if !ok {
		return types.PublicServiceTransportResponse{}, false
	}
	if strings.TrimSpace(strings.ToLower(inferredKind)) != kind {
		return types.PublicServiceTransportResponse{}, false
	}

	serviceNode := resolveServiceNode(tunnel, nodeIndex)
	publicURL := strings.TrimSpace(tunnel.Metadata[serviceMetaPublicURLKey])
	if publicURL == "" {
		publicURL = deriveServicePublicURL(r, tunnel)
	}
	if publicURL == "" {
		return types.PublicServiceTransportResponse{}, false
	}

	p2pURL := strings.TrimSpace(tunnel.Metadata[serviceMetaP2PURLKey])
	if p2pURL == "" {
		p2pURL = deriveServiceP2PURL(tunnel, serviceNode)
	}

	cloudAccess := normalizeServiceAccessPolicy(
		tunnel.Metadata[serviceMetaCloudAccessKey],
		publicURL != "",
	)
	p2pAccess := normalizeServiceAccessPolicy(
		tunnel.Metadata[serviceMetaP2PAccessKey],
		p2pURL != "",
	)

	publicCloudURL := ""
	if cloudAccess == serviceAccessAllUsers {
		publicCloudURL = publicURL
	}
	publicP2PURL := ""
	if p2pAccess == serviceAccessAllUsers {
		publicP2PURL = p2pURL
	}
	if publicCloudURL == "" && publicP2PURL == "" {
		return types.PublicServiceTransportResponse{}, false
	}

	preferredPath := normalizeServicePreferredPath(
		tunnel.Metadata[serviceMetaPreferredPathKey],
		inferredKind,
		publicCloudURL != "",
		publicP2PURL != "",
	)
	manifest := buildServiceTransportManifest(inferredKind, publicCloudURL, publicP2PURL, preferredPath)
	if manifest == nil {
		return types.PublicServiceTransportResponse{}, false
	}

	return types.PublicServiceTransportResponse{
		Key:               key,
		Kind:              inferredKind,
		PublicURL:         publicCloudURL,
		P2PURL:            publicP2PURL,
		PreferredPath:     preferredPath,
		TransportManifest: manifest,
	}, true
}

func resolveServiceNode(tunnel types.TunnelSpec, nodeIndex map[string]types.NodeSummary) types.NodeSummary {
	serviceNodeID := strings.TrimSpace(tunnel.Metadata[serviceMetaP2PNodeIDKey])
	if serviceNodeID == "" {
		serviceNodeID = strings.TrimSpace(tunnel.NodeID)
	}
	if serviceNodeID == "" {
		return types.NodeSummary{}
	}
	if node, ok := nodeIndex[serviceNodeID]; ok {
		return node
	}
	return types.NodeSummary{
		NodeID: serviceNodeID,
		Status: "offline",
	}
}

type inferredServiceIdentity struct {
	key     string
	title   string
	kind    string
	summary string
}

func resolveServiceIdentity(tunnel types.TunnelSpec) (key, title, kind, summary, registrationSource string, ok bool) {
	inferred, inferredOK := inferServiceIdentity(tunnel)
	key = normalizeServiceKey(tunnel.Metadata[serviceMetaKeyKey])
	if key == "" {
		if !inferredOK {
			return "", "", "", "", "", false
		}
		return inferred.key, inferred.title, inferred.kind, inferred.summary, serviceRegistrationSourceInferred, true
	}

	title = strings.TrimSpace(tunnel.Metadata[serviceMetaTitleKey])
	if title == "" {
		if inferredOK {
			title = inferred.title
		} else {
			title = strings.TrimSpace(tunnel.Name)
		}
	}

	kindSeed := strings.TrimSpace(tunnel.Metadata[serviceMetaKindKey])
	if kindSeed == "" && inferredOK {
		kindSeed = inferred.kind
	}
	kind = normalizeServiceKind(kindSeed, key)

	summary = strings.TrimSpace(tunnel.Metadata[serviceMetaSummaryKey])
	if summary == "" && inferredOK {
		summary = inferred.summary
	}

	return key, title, kind, summary, serviceRegistrationSourceMetadata, true
}

func inferServiceIdentity(tunnel types.TunnelSpec) (inferredServiceIdentity, bool) {
	if tunnel.Type != "http" && tunnel.Type != "https" {
		return inferredServiceIdentity{}, false
	}

	name := strings.ToLower(strings.TrimSpace(tunnel.Name))
	domain := strings.ToLower(strings.TrimSpace(tunnel.Domain))

	if strings.Contains(name, "网盘") || strings.Contains(name, "drive") || matchesServiceDomain(domain, "drive") {
		return inferredServiceIdentity{
			key:     "drive",
			title:   "网盘服务",
			kind:    "drive",
			summary: "面向大文件访问，云端保留目录与公开入口，用户端工作台通过 P2P 接入。",
		}, true
	}

	if strings.Contains(name, "图床") || strings.Contains(name, "gallery") || matchesServiceDomain(domain, "gallery", "img", "image") {
		return inferredServiceIdentity{
			key:     "gallery",
			title:   "图床服务",
			kind:    "gallery",
			summary: "公网 HTTPS 继续承担展示与轻量访问，批量上传与下载通过 P2P 工作台接入。",
		}, true
	}

	if strings.Contains(name, "音乐") || strings.Contains(name, "music") || strings.Contains(name, "navidrome") || strings.Contains(name, "subsonic") || matchesServiceDomain(domain, "music", "navidrome", "subsonic", "audio") {
		return inferredServiceIdentity{
			key:     "music",
			title:   "音乐服务",
			kind:    "music",
			summary: "控制面固定走云端 HTTPS，歌曲流、下载与封面优先通过 P2P 获取，异常时快速回退到公网入口。",
		}, true
	}

	return inferredServiceIdentity{}, false
}

func matchesServiceDomain(domain string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		prefix = strings.TrimSpace(strings.ToLower(prefix))
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(domain, prefix+".") || strings.Contains(domain, "."+prefix+".") {
			return true
		}
	}
	return false
}

func normalizeServiceKey(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, " ", "-")
	return value
}

func normalizeServiceKind(value, key string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "drive", "gallery", "music", "app":
		return value
	}
	switch key {
	case "drive":
		return "drive"
	case "gallery":
		return "gallery"
	case "music":
		return "music"
	default:
		return "app"
	}
}

func normalizeServiceAccessPolicy(value string, hasURL bool) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "all_users", "all", "users":
		return serviceAccessAllUsers
	case "admin_only", "admin":
		return serviceAccessAdminOnly
	case "disabled", "none", "off":
		return serviceAccessDisabled
	default:
		if hasURL {
			return serviceAccessAllUsers
		}
		return serviceAccessDisabled
	}
}

func serviceAccessAllowed(policy string, role types.UserRole) bool {
	switch policy {
	case serviceAccessAllUsers:
		return true
	case serviceAccessAdminOnly:
		return role == types.UserRoleAdmin
	default:
		return false
	}
}

func normalizeEndUserP2PAccess(key, kind, policy string, hasP2PURL bool) string {
	if !hasP2PURL {
		return policy
	}
	switch {
	case kind == "drive", kind == "gallery":
		return serviceAccessAllUsers
	case key == "drive", key == "gallery":
		return serviceAccessAllUsers
	default:
		return policy
	}
}

func normalizeServiceURLValue(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return strings.TrimRight(strings.ToLower(value), "/")
	}
	parsed.Fragment = ""
	parsed.RawQuery = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.ToLower(parsed.String())
}

func buildServiceTransportManifest(kind, publicURL, p2pURL, preferredPath string) *types.ServiceTransportManifest {
	if kind != "music" {
		return nil
	}
	return &types.ServiceTransportManifest{
		Version: 1,
		ControlPlane: types.ServiceControlPlaneManifest{
			Mode:    "https_only",
			BaseURL: publicURL,
		},
		DataPlane: types.ServiceDataPlaneManifest{
			PreferredPath: preferredPath,
			CloudBaseURL:  publicURL,
			P2PBaseURL:    p2pURL,
		},
		Capabilities: types.ServiceTransportCapabilities{
			SupportsStream:   true,
			SupportsDownload: true,
			SupportsCoverArt: true,
			SupportsRange:    true,
		},
		ProbePolicy: types.ServiceProbePolicy{
			ConnectTimeoutMs:         1200,
			ReadTimeoutMs:            2000,
			ConsecutiveFailureWindow: 2,
			CooldownSeconds:          30,
			RecoveryProbeIntervalSec: 15,
			RecoverySuccessThreshold: 2,
		},
		RecoveryPolicy: types.ServiceRecoveryPolicy{
			KeepCurrentPlayback:          true,
			FutureRequestsOnlyOnRecover:  true,
			QuickFallbackOnNetworkChange: true,
			AutoRecoverToP2P:             true,
		},
	}
}

func normalizeServicePreferredPath(value, kind string, hasPublicURL, hasP2PURL bool) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "cloud", "p2p", "dual":
		return strings.TrimSpace(strings.ToLower(value))
	}
	switch {
	case hasPublicURL && hasP2PURL:
		if strings.TrimSpace(strings.ToLower(kind)) == "music" {
			return "p2p"
		}
		return "dual"
	case hasP2PURL:
		return "p2p"
	case hasPublicURL:
		return "cloud"
	default:
		return ""
	}
}

func deriveServicePublicURL(r *http.Request, tunnel types.TunnelSpec) string {
	if tunnel.Type == "https" && strings.TrimSpace(tunnel.Domain) != "" {
		return "https://" + strings.TrimSpace(tunnel.Domain)
	}
	if tunnel.Type == "http" && strings.TrimSpace(tunnel.Domain) != "" {
		return "http://" + strings.TrimSpace(tunnel.Domain)
	}
	if tunnel.Type != "http" || tunnel.PublicPort <= 0 {
		return ""
	}
	host := stripPortFromHost(r.Host)
	if host == "" {
		return ""
	}
	return fmt.Sprintf("http://%s:%d", host, tunnel.PublicPort)
}

func deriveServiceP2PURL(tunnel types.TunnelSpec, node types.NodeSummary) string {
	port := serviceP2PTargetPort(tunnel)
	if port <= 0 {
		return ""
	}
	host := normalizeP2PMetricHost(node.LatestMetrics["p2p:ipv4"])
	if host == "" {
		return ""
	}
	switch tunnel.Type {
	case "http", "https":
		baseURL := fmt.Sprintf("http://%s:%d", urlHost(host), port)
		path := normalizeServiceURLPath(tunnel.Metadata[serviceMetaP2PPathKey])
		if path == "" {
			return baseURL
		}
		return baseURL + path
	default:
		return ""
	}
}

func serviceP2PTargetPort(tunnel types.TunnelSpec) int {
	if raw := strings.TrimSpace(tunnel.Metadata[serviceMetaP2PPortKey]); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return tunnel.TargetPort
}

func normalizeServiceURLPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "/") {
		return "/" + value
	}
	return value
}

func normalizeP2PMetricHost(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if ip, _, err := net.ParseCIDR(value); err == nil && ip != nil {
		return ip.String()
	}
	return value
}

func urlHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") && !strings.HasSuffix(host, "]") {
		return "[" + host + "]"
	}
	return host
}

func stripPortFromHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		return parsed
	}
	if strings.HasPrefix(host, "[") && strings.Contains(host, "]") {
		return host
	}
	if strings.Count(host, ":") > 1 {
		return host
	}
	if idx := strings.Index(host, ":"); idx > 0 {
		return host[:idx]
	}
	return host
}
