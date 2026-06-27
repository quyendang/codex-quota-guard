package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

const (
	pluginID      = "codex-quota-guard"
	pluginVersion = "0.1.0"
	providerCodex = "codex"

	defaultRemainingThresholdPercent = 5.0
	defaultFallback429Ban            = 5 * time.Hour
	defaultManualBlockDuration       = time.Hour
)

var (
	currentConfig atomic.Value
	quotaStore    = newQuotaState()
)

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

type pluginConfig struct {
	RemainingThresholdPercent float64       `yaml:"remaining-threshold-percent"`
	Fallback429Ban            time.Duration `yaml:"fallback-429-ban"`
	ManualBlockDuration       time.Duration `yaml:"manual-block-duration"`
}

type rawPluginConfig struct {
	RemainingThresholdPercent *float64 `yaml:"remaining-threshold-percent"`
	Fallback429Ban            string   `yaml:"fallback-429-ban"`
	ManualBlockDuration       string   `yaml:"manual-block-duration"`
}

type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginapi.Metadata       `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	UsagePlugin   bool `json:"usage_plugin"`
	Scheduler     bool `json:"scheduler"`
	ManagementAPI bool `json:"management_api"`
}

type managementRegistrationResponse struct {
	Routes    []managementRoute    `json:"routes,omitempty"`
	Resources []managementResource `json:"resources,omitempty"`
}

type managementRoute struct {
	Method      string `json:"Method"`
	Path        string `json:"Path"`
	Description string `json:"Description,omitempty"`
}

type managementResource struct {
	Path        string `json:"Path"`
	Menu        string `json:"Menu"`
	Description string `json:"Description"`
}

type managementRequest struct {
	Method string
	Path   string
	Query  url.Values
	Body   []byte
}

func init() {
	currentConfig.Store(defaultPluginConfig())
}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		if errConfigure := configure(request); errConfigure != nil {
			return nil, errConfigure
		}
		return okEnvelope(pluginRegistration())
	case pluginabi.MethodUsageHandle:
		return handleUsage(request)
	case pluginabi.MethodSchedulerPick:
		return handleSchedulerPick(request)
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegistration())
	case pluginabi.MethodManagementHandle:
		return handleManagement(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func configure(raw []byte) error {
	var req lifecycleRequest
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			return errUnmarshal
		}
	}
	cfg := defaultPluginConfig()
	if len(req.ConfigYAML) > 0 {
		decoded, errDecode := decodeConfig(req.ConfigYAML)
		if errDecode != nil {
			return errDecode
		}
		cfg = decoded
	}
	currentConfig.Store(cfg)
	return nil
}

func defaultPluginConfig() pluginConfig {
	return pluginConfig{
		RemainingThresholdPercent: defaultRemainingThresholdPercent,
		Fallback429Ban:            defaultFallback429Ban,
		ManualBlockDuration:       defaultManualBlockDuration,
	}
}

func decodeConfig(raw []byte) (pluginConfig, error) {
	cfg := defaultPluginConfig()
	var decoded rawPluginConfig
	if errUnmarshal := yaml.Unmarshal(raw, &decoded); errUnmarshal != nil {
		return pluginConfig{}, errUnmarshal
	}
	if decoded.RemainingThresholdPercent != nil {
		cfg.RemainingThresholdPercent = *decoded.RemainingThresholdPercent
	}
	if cfg.RemainingThresholdPercent < 0 || cfg.RemainingThresholdPercent > 100 {
		return pluginConfig{}, fmt.Errorf("remaining-threshold-percent must be between 0 and 100")
	}
	if strings.TrimSpace(decoded.Fallback429Ban) != "" {
		duration, errParse := time.ParseDuration(strings.TrimSpace(decoded.Fallback429Ban))
		if errParse != nil {
			return pluginConfig{}, fmt.Errorf("parse fallback-429-ban: %w", errParse)
		}
		cfg.Fallback429Ban = duration
	}
	if cfg.Fallback429Ban <= 0 {
		return pluginConfig{}, fmt.Errorf("fallback-429-ban must be positive")
	}
	if strings.TrimSpace(decoded.ManualBlockDuration) != "" {
		duration, errParse := time.ParseDuration(strings.TrimSpace(decoded.ManualBlockDuration))
		if errParse != nil {
			return pluginConfig{}, fmt.Errorf("parse manual-block-duration: %w", errParse)
		}
		cfg.ManualBlockDuration = duration
	}
	if cfg.ManualBlockDuration <= 0 {
		return pluginConfig{}, fmt.Errorf("manual-block-duration must be positive")
	}
	return cfg, nil
}

func loadedConfig() pluginConfig {
	if cfg, ok := currentConfig.Load().(pluginConfig); ok {
		return cfg
	}
	return defaultPluginConfig()
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             pluginID,
			Version:          pluginVersion,
			Author:           "router-for-me",
			GitHubRepository: "https://github.com/router-for-me/CLIProxyAPI",
			Logo:             "https://raw.githubusercontent.com/router-for-me/CLIProxyAPI/main/docs/logo.png",
			ConfigFields: []pluginapi.ConfigField{
				{
					Name:        "remaining-threshold-percent",
					Type:        pluginapi.ConfigFieldTypeNumber,
					Description: "Soft-blocks Codex credentials when a quota window has this percent or less remaining. Default: 5.",
				},
				{
					Name:        "fallback-429-ban",
					Type:        pluginapi.ConfigFieldTypeString,
					Description: "Fallback soft-block duration for Codex 429 usage limits without reset headers. Default: 5h.",
				},
				{
					Name:        "manual-block-duration",
					Type:        pluginapi.ConfigFieldTypeString,
					Description: "Default duration used by the control panel manual block action. Default: 1h.",
				},
			},
		},
		Capabilities: registrationCapabilities{
			UsagePlugin:   true,
			Scheduler:     true,
			ManagementAPI: true,
		},
	}
}

func managementRegistration() managementRegistrationResponse {
	return managementRegistrationResponse{
		Resources: []managementResource{{
			Path:        "/status",
			Menu:        "Codex Quota Guard",
			Description: "Shows Codex quota windows, soft blocks, reset times, and manual controls.",
		}},
		Routes: []managementRoute{
			{Method: http.MethodGet, Path: "/codex-quota-guard/state", Description: "Returns Codex quota guard state."},
			{Method: http.MethodPost, Path: "/codex-quota-guard/block", Description: "Soft-blocks one Codex credential in plugin state."},
			{Method: http.MethodPost, Path: "/codex-quota-guard/unblock", Description: "Clears one Codex credential soft block."},
			{Method: http.MethodPost, Path: "/codex-quota-guard/clear", Description: "Clears all plugin quota state."},
		},
	}
}

func handleUsage(raw []byte) ([]byte, error) {
	var record pluginapi.UsageRecord
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &record); errUnmarshal != nil {
			return nil, errUnmarshal
		}
	}
	if !strings.EqualFold(record.Provider, providerCodex) || strings.TrimSpace(record.AuthID) == "" {
		return okEnvelope(map[string]any{})
	}
	cfg := loadedConfig()
	quotaStore.applyUsage(record, cfg, time.Now())
	return okEnvelope(map[string]any{})
}

func handleSchedulerPick(raw []byte) ([]byte, error) {
	var req pluginapi.SchedulerPickRequest
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	available, filtered := quotaStore.availableCandidates(req.Candidates, time.Now())
	if len(available) == 0 {
		return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
	}
	if filtered == 0 {
		return okEnvelope(pluginapi.SchedulerPickResponse{
			DelegateBuiltin: pluginapi.SchedulerBuiltinRoundRobin,
			Handled:         true,
		})
	}
	chosen := chooseCandidate(available)
	quotaStore.recordDecision(schedulerDecision{
		At:           time.Now(),
		Model:        req.Model,
		ChosenAuthID: chosen.ID,
		Filtered:     filtered,
		Candidates:   len(req.Candidates),
	})
	return okEnvelope(pluginapi.SchedulerPickResponse{AuthID: chosen.ID, Handled: true})
}

func chooseCandidate(candidates []pluginapi.SchedulerAuthCandidate) pluginapi.SchedulerAuthCandidate {
	chosen := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.Priority > chosen.Priority {
			chosen = candidate
		}
	}
	return chosen
}

func handleManagement(raw []byte) ([]byte, error) {
	var req managementRequest
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			return nil, errUnmarshal
		}
	}
	path := strings.TrimSpace(req.Path)
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	switch {
	case method == http.MethodGet && strings.HasSuffix(path, "/status"):
		if strings.EqualFold(strings.TrimSpace(req.Query.Get("format")), "json") {
			return jsonManagementResponse(http.StatusOK, quotaStore.snapshot(time.Now()))
		}
		return okEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Body:       renderStatusPage(quotaStore.snapshot(time.Now()), loadedConfig()),
		})
	case method == http.MethodGet && strings.HasSuffix(path, "/codex-quota-guard/state"):
		return jsonManagementResponse(http.StatusOK, quotaStore.snapshot(time.Now()))
	case method == http.MethodPost && strings.HasSuffix(path, "/codex-quota-guard/block"):
		return handleManualBlock(req.Body)
	case method == http.MethodPost && strings.HasSuffix(path, "/codex-quota-guard/unblock"):
		return handleManualUnblock(req.Body)
	case method == http.MethodPost && strings.HasSuffix(path, "/codex-quota-guard/clear"):
		quotaStore.clear()
		return jsonManagementResponse(http.StatusOK, map[string]any{"status": "ok"})
	default:
		return okEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusNotFound,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       []byte(`{"error":"route not found"}`),
		})
	}
}

type manualBlockRequest struct {
	AuthID   string `json:"auth_id"`
	Duration string `json:"duration"`
	Reason   string `json:"reason"`
}

type manualUnblockRequest struct {
	AuthID string `json:"auth_id"`
}

func handleManualBlock(body []byte) ([]byte, error) {
	var req manualBlockRequest
	if errUnmarshal := json.Unmarshal(body, &req); errUnmarshal != nil {
		return jsonManagementResponse(http.StatusBadRequest, map[string]any{"error": "invalid request body"})
	}
	authID := strings.TrimSpace(req.AuthID)
	if authID == "" {
		return jsonManagementResponse(http.StatusBadRequest, map[string]any{"error": "auth_id is required"})
	}
	duration := loadedConfig().ManualBlockDuration
	if strings.TrimSpace(req.Duration) != "" {
		parsed, errParse := time.ParseDuration(strings.TrimSpace(req.Duration))
		if errParse != nil || parsed <= 0 {
			return jsonManagementResponse(http.StatusBadRequest, map[string]any{"error": "duration must be a positive Go duration, for example 30m or 2h"})
		}
		duration = parsed
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "manual block"
	}
	quotaStore.manualBlock(authID, time.Now().Add(duration), reason)
	return jsonManagementResponse(http.StatusOK, map[string]any{"status": "ok", "auth_id": authID})
}

func handleManualUnblock(body []byte) ([]byte, error) {
	var req manualUnblockRequest
	if errUnmarshal := json.Unmarshal(body, &req); errUnmarshal != nil {
		return jsonManagementResponse(http.StatusBadRequest, map[string]any{"error": "invalid request body"})
	}
	authID := strings.TrimSpace(req.AuthID)
	if authID == "" {
		return jsonManagementResponse(http.StatusBadRequest, map[string]any{"error": "auth_id is required"})
	}
	quotaStore.unblock(authID)
	return jsonManagementResponse(http.StatusOK, map[string]any{"status": "ok", "auth_id": authID})
}

func jsonManagementResponse(status int, v any) ([]byte, error) {
	body, errMarshal := json.Marshal(v)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return okEnvelope(pluginapi.ManagementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       body,
	})
}

func logStateChange(message string, fields ...any) {
	slog.Info("codex-quota-guard: "+message, fields...)
}

func sortedCredentialStates(states []credentialView) []credentialView {
	sort.Slice(states, func(i, j int) bool {
		if states[i].Status != states[j].Status {
			return statusRank(states[i].Status) < statusRank(states[j].Status)
		}
		return states[i].AuthID < states[j].AuthID
	})
	return states
}

func statusRank(status string) int {
	switch status {
	case statusCooling:
		return 0
	case statusManualBlock:
		return 1
	case statusNearLimit:
		return 2
	case statusUsable:
		return 3
	default:
		return 4
	}
}
