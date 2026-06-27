package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const authFileMarkerKey = "codex_quota_guard"

type hostAuthListResponse struct {
	Files []pluginapi.HostAuthFileEntry `json:"files"`
}

type authFileMarker struct {
	DisabledByPlugin bool   `json:"disabled_by_plugin"`
	AuthID           string `json:"auth_id,omitempty"`
	BlockedUntil     string `json:"blocked_until,omitempty"`
	Reason           string `json:"reason,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

type authFileMutationResult struct {
	AuthID       string
	Name         string
	Path         string
	Disabled     bool
	Managed      bool
	Changed      bool
	Message      string
	BlockedUntil time.Time
	Reason       string
}

func disableAuthFileForBlock(action authBlockAction, now time.Time) (authFileMutationResult, error) {
	entry, ok, errResolve := resolveAuthFile(action.AuthID)
	if errResolve != nil {
		return authFileMutationResult{AuthID: action.AuthID}, errResolve
	}
	if !ok {
		return authFileMutationResult{AuthID: action.AuthID}, fmt.Errorf("auth file not found for auth_id %s", action.AuthID)
	}
	if entry.RuntimeOnly {
		return authFileMutationResult{AuthID: action.AuthID, Name: entry.Name, Path: entry.Path}, fmt.Errorf("auth %s is runtime-only", action.AuthID)
	}
	if strings.TrimSpace(entry.AuthIndex) == "" {
		return authFileMutationResult{AuthID: action.AuthID, Name: entry.Name, Path: entry.Path}, fmt.Errorf("auth %s has no auth_index", action.AuthID)
	}
	got, errGet := callHostAuthGet(entry.AuthIndex)
	if errGet != nil {
		return authFileMutationResult{AuthID: action.AuthID, Name: entry.Name, Path: entry.Path}, errGet
	}
	raw := got.JSON
	if len(raw) == 0 {
		return authFileMutationResult{AuthID: action.AuthID, Name: entry.Name, Path: entry.Path}, fmt.Errorf("auth %s returned empty JSON", action.AuthID)
	}
	name := firstNonEmpty(got.Name, entry.Name)
	path := firstNonEmpty(got.Path, entry.Path)
	nextRaw, changed, managed, message, errMutate := setAuthFileDisabled(raw, true, authFileMarker{
		DisabledByPlugin: true,
		AuthID:           action.AuthID,
		BlockedUntil:     action.Until.Format(time.RFC3339),
		Reason:           action.Reason,
		UpdatedAt:        now.Format(time.RFC3339),
	})
	result := authFileMutationResult{
		AuthID:       action.AuthID,
		Name:         name,
		Path:         path,
		Disabled:     true,
		Managed:      managed,
		Changed:      changed,
		Message:      message,
		BlockedUntil: action.Until,
		Reason:       action.Reason,
	}
	if errMutate != nil {
		return result, errMutate
	}
	if !managed || !changed {
		return result, nil
	}
	if _, errSave := callHostAuthSave(name, nextRaw); errSave != nil {
		return result, errSave
	}
	return result, nil
}

func enableAuthFileIfOwned(authID string, now time.Time) (authFileMutationResult, error) {
	entry, ok, errResolve := resolveAuthFile(authID)
	if errResolve != nil {
		return authFileMutationResult{AuthID: authID}, errResolve
	}
	if !ok {
		return authFileMutationResult{AuthID: authID}, fmt.Errorf("auth file not found for auth_id %s", authID)
	}
	if strings.TrimSpace(entry.AuthIndex) == "" {
		return authFileMutationResult{AuthID: authID, Name: entry.Name, Path: entry.Path}, fmt.Errorf("auth %s has no auth_index", authID)
	}
	got, errGet := callHostAuthGet(entry.AuthIndex)
	if errGet != nil {
		return authFileMutationResult{AuthID: authID, Name: entry.Name, Path: entry.Path}, errGet
	}
	name := firstNonEmpty(got.Name, entry.Name)
	path := firstNonEmpty(got.Path, entry.Path)
	nextRaw, changed, managed, message, errMutate := setAuthFileDisabled(got.JSON, false, authFileMarker{
		DisabledByPlugin: true,
		AuthID:           authID,
		UpdatedAt:        now.Format(time.RFC3339),
	})
	result := authFileMutationResult{
		AuthID:   authID,
		Name:     name,
		Path:     path,
		Disabled: false,
		Managed:  managed,
		Changed:  changed,
		Message:  message,
	}
	if errMutate != nil {
		return result, errMutate
	}
	if !managed || !changed {
		return result, nil
	}
	if _, errSave := callHostAuthSave(name, nextRaw); errSave != nil {
		return result, errSave
	}
	return result, nil
}

func reconcileManagedAuthFiles(now time.Time) {
	list, errList := callHostAuthList()
	if errList != nil {
		quotaStore.recordHostError("", errList)
		return
	}
	for _, entry := range list.Files {
		if entry.RuntimeOnly || !strings.EqualFold(entry.Provider, providerCodex) {
			continue
		}
		if strings.TrimSpace(entry.AuthIndex) == "" {
			continue
		}
		got, errGet := callHostAuthGet(entry.AuthIndex)
		if errGet != nil {
			quotaStore.recordHostError(firstNonEmpty(entry.ID, entry.AuthIndex), errGet)
			continue
		}
		marker, ok := markerFromAuthJSON(got.JSON)
		if !ok || !marker.DisabledByPlugin {
			continue
		}
		authID := firstNonEmpty(marker.AuthID, entry.ID, entry.AuthIndex)
		blockedUntil, _ := time.Parse(time.RFC3339, marker.BlockedUntil)
		quotaStore.recordAuthFileBlock(authID, authFileMutationResult{
			AuthID:       authID,
			Name:         firstNonEmpty(got.Name, entry.Name),
			Path:         firstNonEmpty(got.Path, entry.Path),
			Disabled:     entry.Disabled,
			Managed:      true,
			BlockedUntil: blockedUntil,
			Reason:       marker.Reason,
		})
		if !blockedUntil.IsZero() && blockedUntil.After(now) {
			continue
		}
		result, errEnable := enableAuthFileIfOwned(authID, now)
		quotaStore.recordAuthFileEnableResult(authID, result, errEnable)
	}
}

func resolveAuthFile(authID string) (pluginapi.HostAuthFileEntry, bool, error) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return pluginapi.HostAuthFileEntry{}, false, fmt.Errorf("auth_id is required")
	}
	list, errList := callHostAuthList()
	if errList != nil {
		return pluginapi.HostAuthFileEntry{}, false, errList
	}
	for _, entry := range list.Files {
		if !strings.EqualFold(entry.Provider, providerCodex) {
			continue
		}
		if entry.ID == authID || entry.AuthIndex == authID || entry.Name == authID {
			return entry, true, nil
		}
	}
	return pluginapi.HostAuthFileEntry{}, false, nil
}

func setAuthFileDisabled(raw json.RawMessage, disabled bool, marker authFileMarker) (json.RawMessage, bool, bool, string, error) {
	var doc map[string]any
	if errUnmarshal := json.Unmarshal(raw, &doc); errUnmarshal != nil {
		return nil, false, false, "", fmt.Errorf("decode auth JSON: %w", errUnmarshal)
	}
	existingDisabled, _ := doc["disabled"].(bool)
	existingMarker, markerOwned := markerFromDoc(doc)
	if disabled {
		if existingDisabled && !markerOwned {
			return raw, false, false, "auth file already disabled outside plugin", nil
		}
		doc["disabled"] = true
		doc[authFileMarkerKey] = marker
		next, errMarshal := json.MarshalIndent(doc, "", "  ")
		if errMarshal != nil {
			return nil, false, true, "", errMarshal
		}
		changed := !existingDisabled || !markerOwned || existingMarker.BlockedUntil != marker.BlockedUntil || existingMarker.Reason != marker.Reason
		return append(next, '\n'), changed, true, "auth file disabled by plugin", nil
	}
	if !markerOwned {
		return raw, false, false, "auth file marker is not owned by plugin", nil
	}
	doc["disabled"] = false
	delete(doc, authFileMarkerKey)
	next, errMarshal := json.MarshalIndent(doc, "", "  ")
	if errMarshal != nil {
		return nil, false, true, "", errMarshal
	}
	return append(next, '\n'), existingDisabled || markerOwned, true, "auth file enabled by plugin", nil
}

func markerFromAuthJSON(raw json.RawMessage) (authFileMarker, bool) {
	var doc map[string]any
	if errUnmarshal := json.Unmarshal(raw, &doc); errUnmarshal != nil {
		return authFileMarker{}, false
	}
	return markerFromDoc(doc)
}

func markerFromDoc(doc map[string]any) (authFileMarker, bool) {
	rawMarker, ok := doc[authFileMarkerKey]
	if !ok {
		return authFileMarker{}, false
	}
	markerBytes, errMarshal := json.Marshal(rawMarker)
	if errMarshal != nil {
		return authFileMarker{}, false
	}
	var marker authFileMarker
	if errUnmarshal := json.Unmarshal(markerBytes, &marker); errUnmarshal != nil {
		return authFileMarker{}, false
	}
	return marker, marker.DisabledByPlugin
}

func callHostAuthList() (hostAuthListResponse, error) {
	result, errCall := callHost(pluginabi.MethodHostAuthList, map[string]any{})
	if errCall != nil {
		return hostAuthListResponse{}, errCall
	}
	var resp hostAuthListResponse
	if errUnmarshal := json.Unmarshal(result, &resp); errUnmarshal != nil {
		return hostAuthListResponse{}, fmt.Errorf("decode host.auth.list result: %w", errUnmarshal)
	}
	return resp, nil
}

func callHostAuthGet(authIndex string) (pluginapi.HostAuthGetResponse, error) {
	result, errCall := callHost(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if errCall != nil {
		return pluginapi.HostAuthGetResponse{}, errCall
	}
	var resp pluginapi.HostAuthGetResponse
	if errUnmarshal := json.Unmarshal(result, &resp); errUnmarshal != nil {
		return pluginapi.HostAuthGetResponse{}, fmt.Errorf("decode host.auth.get result: %w", errUnmarshal)
	}
	return resp, nil
}

func callHostAuthGetRuntime(authIndex string) (pluginapi.HostAuthGetRuntimeResponse, error) {
	result, errCall := callHost(pluginabi.MethodHostAuthGetRuntime, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if errCall != nil {
		return pluginapi.HostAuthGetRuntimeResponse{}, errCall
	}
	var resp pluginapi.HostAuthGetRuntimeResponse
	if errUnmarshal := json.Unmarshal(result, &resp); errUnmarshal != nil {
		return pluginapi.HostAuthGetRuntimeResponse{}, fmt.Errorf("decode host.auth.get_runtime result: %w", errUnmarshal)
	}
	return resp, nil
}

func callHostAuthSave(name string, rawJSON json.RawMessage) (pluginapi.HostAuthSaveResponse, error) {
	result, errCall := callHost(pluginabi.MethodHostAuthSave, pluginapi.HostAuthSaveRequest{
		Name: name,
		JSON: rawJSON,
	})
	if errCall != nil {
		return pluginapi.HostAuthSaveResponse{}, errCall
	}
	var resp pluginapi.HostAuthSaveResponse
	if errUnmarshal := json.Unmarshal(result, &resp); errUnmarshal != nil {
		return pluginapi.HostAuthSaveResponse{}, fmt.Errorf("decode host.auth.save result: %w", errUnmarshal)
	}
	return resp, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
