package api

import (
	"fmt"
	"net"
	"net/http"
	"sort"
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
	serviceMetaCloudAccessKey   = "serviceCloudAccess"
	serviceMetaP2PAccessKey     = "serviceP2PAccess"
	serviceMetaPreferredPathKey = "servicePreferredPath"

	serviceAccessAllUsers  = "all_users"
	serviceAccessAdminOnly = "admin_only"
	serviceAccessDisabled  = "disabled"
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
		entry, ok := userServiceEntryFromTunnel(r, user, tunnel, nodeIndex[tunnel.NodeID])
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

func userServiceEntryFromTunnel(
	r *http.Request,
	user types.UserSummary,
	tunnel types.TunnelSpec,
	node types.NodeSummary,
) (types.UserServiceEntry, bool) {
	key := normalizeServiceKey(tunnel.Metadata[serviceMetaKeyKey])
	if key == "" {
		return types.UserServiceEntry{}, false
	}

	publicURL := strings.TrimSpace(tunnel.Metadata[serviceMetaPublicURLKey])
	if publicURL == "" {
		publicURL = deriveServicePublicURL(r, tunnel)
	}
	p2pURL := strings.TrimSpace(tunnel.Metadata[serviceMetaP2PURLKey])

	cloudAccess := normalizeServiceAccessPolicy(tunnel.Metadata[serviceMetaCloudAccessKey], publicURL != "")
	p2pAccess := normalizeServiceAccessPolicy(tunnel.Metadata[serviceMetaP2PAccessKey], p2pURL != "")
	preferredPath := normalizeServicePreferredPath(tunnel.Metadata[serviceMetaPreferredPathKey], publicURL != "", p2pURL != "")

	title := strings.TrimSpace(tunnel.Metadata[serviceMetaTitleKey])
	if title == "" {
		title = strings.TrimSpace(tunnel.Name)
	}
	kind := normalizeServiceKind(tunnel.Metadata[serviceMetaKindKey], key)
	summary := strings.TrimSpace(tunnel.Metadata[serviceMetaSummaryKey])

	return types.UserServiceEntry{
		Key:             key,
		Title:           title,
		Kind:            kind,
		Summary:         summary,
		NodeID:          tunnel.NodeID,
		NodeName:        node.NodeName,
		NodeStatus:      node.Status,
		TunnelID:        tunnel.ID,
		TunnelName:      tunnel.Name,
		TunnelType:      tunnel.Type,
		TunnelStatus:    tunnel.Status,
		TransportPolicy: tunnel.TransportPolicy,
		RuntimePath:     tunnel.RuntimePath,
		RuntimeState:    tunnel.RuntimeState,
		HealthStatus:    string(tunnel.HealthStatus),
		PublicURL:       publicURL,
		P2PURL:          p2pURL,
		CloudAccess:     cloudAccess,
		P2PAccess:       p2pAccess,
		CloudAllowed:    serviceAccessAllowed(cloudAccess, user.Role),
		P2PAllowed:      serviceAccessAllowed(p2pAccess, user.Role),
		PreferredPath:   preferredPath,
	}, true
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
	case "drive", "gallery", "app":
		return value
	}
	switch key {
	case "drive":
		return "drive"
	case "gallery":
		return "gallery"
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

func normalizeServicePreferredPath(value string, hasPublicURL, hasP2PURL bool) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "cloud", "p2p", "dual":
		return strings.TrimSpace(strings.ToLower(value))
	}
	switch {
	case hasPublicURL && hasP2PURL:
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
