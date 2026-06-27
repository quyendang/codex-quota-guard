package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestParseWindowHeaders(t *testing.T) {
	reset := time.Unix(1800000000, 0)
	headers := http.Header{
		"X-Codex-Primary-Window-Minutes": []string{"300"},
		"X-Codex-Primary-Used-Percent":   []string{"97.5"},
		"X-Codex-Primary-Reset-At":       []string{"1800000000"},
	}
	window, ok := parseWindowHeaders(headers, "x-codex-primary-", time.Unix(1700000000, 0))
	if !ok {
		t.Fatal("parseWindowHeaders() ok = false")
	}
	if window.WindowMinutes != 300 || window.UsedPercent != 97.5 || !window.ResetAt.Equal(reset) {
		t.Fatalf("window = %+v", window)
	}
}

func TestApplyUsageBlocksAtThreshold(t *testing.T) {
	now := time.Unix(1700000000, 0)
	reset := now.Add(2 * time.Hour)
	store := newQuotaState()
	store.applyUsage(pluginapi.UsageRecord{
		Provider: providerCodex,
		AuthID:   "auth-1",
		ResponseHeaders: http.Header{
			"X-Codex-Primary-Window-Minutes": []string{"300"},
			"X-Codex-Primary-Used-Percent":   []string{"95"},
			"X-Codex-Primary-Reset-At":       []string{itoa(reset.Unix())},
		},
	}, defaultPluginConfig(), now)

	snap := store.snapshot(now)
	if len(snap.Credentials) != 1 {
		t.Fatalf("credentials = %d, want 1", len(snap.Credentials))
	}
	got := snap.Credentials[0]
	if got.Status != statusCooling || got.BlockReason != blockReasonPrimaryThreshold || !got.BlockedUntil.Equal(reset) {
		t.Fatalf("credential = %+v", got)
	}
}

func TestApplyUsageBlocksAtRemainingThirtyPercent(t *testing.T) {
	now := time.Unix(1700000000, 0)
	reset := now.Add(2 * time.Hour)
	cfg := defaultPluginConfig()
	cfg.RemainingThresholdPercent = 30
	store := newQuotaState()
	action := store.applyUsage(pluginapi.UsageRecord{
		Provider: providerCodex,
		AuthID:   "auth-70",
		ResponseHeaders: http.Header{
			"X-Codex-Primary-Window-Minutes": []string{"300"},
			"X-Codex-Primary-Used-Percent":   []string{"70"},
			"X-Codex-Primary-Reset-At":       []string{itoa(reset.Unix())},
		},
	}, cfg, now)

	if action == nil || action.AuthID != "auth-70" || !action.Until.Equal(reset) || action.Reason != blockReasonPrimaryThreshold {
		t.Fatalf("action = %+v, want auth-file block action until %s", action, reset)
	}
	got := store.snapshot(now).Credentials[0]
	if got.Status != statusCooling || got.BlockReason != blockReasonPrimaryThreshold {
		t.Fatalf("credential = %+v", got)
	}
}

func TestUsageLimitChoosesLaterHeaderReset(t *testing.T) {
	now := time.Unix(1700000000, 0)
	bodyReset := now.Add(time.Hour)
	weeklyReset := now.Add(24 * time.Hour)
	store := newQuotaState()
	store.applyUsage(pluginapi.UsageRecord{
		Provider: providerCodex,
		AuthID:   "auth-weekly",
		Failed:   true,
		Failure: pluginapi.UsageFailure{
			StatusCode: http.StatusTooManyRequests,
			Body:       `{"error":{"type":"usage_limit_reached","resets_at":` + itoa(bodyReset.Unix()) + `}}`,
		},
		ResponseHeaders: http.Header{
			"X-Codex-Secondary-Window-Minutes": []string{"10080"},
			"X-Codex-Secondary-Used-Percent":   []string{"100"},
			"X-Codex-Secondary-Reset-At":       []string{itoa(weeklyReset.Unix())},
		},
	}, defaultPluginConfig(), now)

	got := store.snapshot(now).Credentials[0]
	if !got.BlockedUntil.Equal(weeklyReset) || got.BlockReason != blockReasonSecondaryThreshold {
		t.Fatalf("blocked until %s reason %q, want %s reason %q", got.BlockedUntil, got.BlockReason, weeklyReset, blockReasonSecondaryThreshold)
	}
}

func TestUsageLimitUsesKnownWindowResetWhenUsedPercentMissing(t *testing.T) {
	now := time.Unix(1700000000, 0)
	reset := now.Add(5 * time.Hour)
	store := newQuotaState()
	store.applyUsage(pluginapi.UsageRecord{
		Provider: providerCodex,
		AuthID:   "auth-window-reset",
		Failed:   true,
		Failure: pluginapi.UsageFailure{
			StatusCode: http.StatusTooManyRequests,
			Body:       `{"error":{"type":"usage_limit_reached"}}`,
		},
		ResponseHeaders: http.Header{
			"X-Codex-Primary-Window-Minutes": []string{"300"},
			"X-Codex-Primary-Reset-At":       []string{itoa(reset.Unix())},
		},
	}, defaultPluginConfig(), now)

	got := store.snapshot(now).Credentials[0]
	if !got.BlockedUntil.Equal(reset) || got.BlockReason != blockReasonPrimaryThreshold {
		t.Fatalf("blocked until %s reason %q, want %s reason %q", got.BlockedUntil, got.BlockReason, reset, blockReasonPrimaryThreshold)
	}
}

func TestResetFromWindowsReasonTracksLaterReset(t *testing.T) {
	now := time.Unix(1700000000, 0)
	primaryReset := now.Add(24 * time.Hour)
	secondaryReset := now.Add(time.Hour)
	state := &credentialState{
		Primary:   windowState{UsedPercent: 100, ResetAt: primaryReset},
		Secondary: windowState{UsedPercent: 100, ResetAt: secondaryReset},
	}
	reset, reason, ok := resetFromWindows(state, 95, now)
	if !ok || !reset.Equal(primaryReset) || reason != blockReasonPrimaryThreshold {
		t.Fatalf("reset=%s reason=%q ok=%v, want %s reason %q", reset, reason, ok, primaryReset, blockReasonPrimaryThreshold)
	}
}

func TestUsageLimitFallback429Ban(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cfg := defaultPluginConfig()
	cfg.Fallback429Ban = 30 * time.Minute
	store := newQuotaState()
	store.applyUsage(pluginapi.UsageRecord{
		Provider: providerCodex,
		AuthID:   "auth-fallback",
		Failed:   true,
		Failure: pluginapi.UsageFailure{
			StatusCode: http.StatusTooManyRequests,
			Body:       `{"error":{"type":"usage_limit_reached"}}`,
		},
	}, cfg, now)

	got := store.snapshot(now).Credentials[0]
	if !got.BlockedUntil.Equal(now.Add(30*time.Minute)) || got.BlockReason != blockReasonFallback429 {
		t.Fatalf("credential = %+v", got)
	}
}

func TestSuccessfulRecoveredHeadersClearAutoBlock(t *testing.T) {
	now := time.Unix(1700000000, 0)
	reset := now.Add(2 * time.Hour)
	store := newQuotaState()
	store.applyUsage(pluginapi.UsageRecord{
		Provider: providerCodex,
		AuthID:   "auth-recovered",
		ResponseHeaders: http.Header{
			"X-Codex-Primary-Window-Minutes": []string{"300"},
			"X-Codex-Primary-Used-Percent":   []string{"95"},
			"X-Codex-Primary-Reset-At":       []string{itoa(reset.Unix())},
		},
	}, defaultPluginConfig(), now)
	if got := store.snapshot(now).Credentials[0]; got.Status != statusCooling {
		t.Fatalf("initial status = %s, want cooling", got.Status)
	}

	store.applyUsage(pluginapi.UsageRecord{
		Provider: providerCodex,
		AuthID:   "auth-recovered",
		ResponseHeaders: http.Header{
			"X-Codex-Primary-Window-Minutes": []string{"300"},
			"X-Codex-Primary-Used-Percent":   []string{"30"},
			"X-Codex-Primary-Reset-At":       []string{itoa(reset.Unix())},
		},
	}, defaultPluginConfig(), now.Add(time.Minute))

	got := store.snapshot(now.Add(time.Minute)).Credentials[0]
	if got.Status != statusUsable || !got.BlockedUntil.IsZero() {
		t.Fatalf("recovered credential = %+v", got)
	}
}

func TestSchedulerFiltersAndLazyUnblocks(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := newQuotaState()
	store.manualBlock("blocked", now.Add(time.Hour), "manual block")
	candidates := []pluginapi.SchedulerAuthCandidate{
		{ID: "blocked", Provider: providerCodex, Priority: 100},
		{ID: "open", Provider: providerCodex, Priority: 10},
	}
	available, filtered := store.availableCandidates(candidates, now)
	if filtered != 1 || len(available) != 1 || available[0].ID != "open" {
		t.Fatalf("available = %+v filtered = %d", available, filtered)
	}

	available, filtered = store.availableCandidates(candidates, now.Add(2*time.Hour))
	if filtered != 0 || len(available) != 2 {
		t.Fatalf("after reset available = %+v filtered = %d", available, filtered)
	}
	if got := store.snapshot(now.Add(2 * time.Hour)).Credentials[0]; got.Status == statusManualBlock || !got.BlockedUntil.IsZero() {
		t.Fatalf("expected lazy unblock, got %+v", got)
	}
}

func TestSchedulerAllFilteredDeclinesSoAuthFileDisableMustEnforce(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := newQuotaState()
	store.manualBlock("blocked", now.Add(time.Hour), "manual block")
	candidates := []pluginapi.SchedulerAuthCandidate{
		{ID: "blocked", Provider: providerCodex, Priority: 100},
	}
	available, filtered := store.availableCandidates(candidates, now)
	if filtered != 1 || len(available) != 0 {
		t.Fatalf("available = %+v filtered = %d, want all filtered", available, filtered)
	}
}

func TestSetAuthFileDisabledAddsOwnedMarker(t *testing.T) {
	raw := json.RawMessage(`{"type":"codex","email":"a@example.com","disabled":false}`)
	until := time.Unix(1700003600, 0).UTC()
	next, changed, managed, message, errMutate := setAuthFileDisabled(raw, true, authFileMarker{
		DisabledByPlugin: true,
		AuthID:           "auth-1",
		BlockedUntil:     until.Format(time.RFC3339),
		Reason:           blockReasonPrimaryThreshold,
		UpdatedAt:        time.Unix(1700000000, 0).UTC().Format(time.RFC3339),
	})
	if errMutate != nil {
		t.Fatal(errMutate)
	}
	if !changed || !managed || !strings.Contains(message, "disabled") {
		t.Fatalf("changed=%v managed=%v message=%q", changed, managed, message)
	}
	var doc map[string]any
	if errUnmarshal := json.Unmarshal(next, &doc); errUnmarshal != nil {
		t.Fatal(errUnmarshal)
	}
	if disabled, _ := doc["disabled"].(bool); !disabled {
		t.Fatalf("disabled = %v, want true", doc["disabled"])
	}
	marker, ok := markerFromDoc(doc)
	if !ok || !marker.DisabledByPlugin || marker.AuthID != "auth-1" || marker.BlockedUntil != until.Format(time.RFC3339) {
		t.Fatalf("marker = %+v ok=%v", marker, ok)
	}
}

func TestSetAuthFileDisabledDoesNotOwnExternalDisable(t *testing.T) {
	raw := json.RawMessage(`{"type":"codex","email":"a@example.com","disabled":true}`)
	next, changed, managed, _, errMutate := setAuthFileDisabled(raw, true, authFileMarker{
		DisabledByPlugin: true,
		AuthID:           "auth-1",
	})
	if errMutate != nil {
		t.Fatal(errMutate)
	}
	if changed || managed || string(next) != string(raw) {
		t.Fatalf("changed=%v managed=%v next=%s", changed, managed, next)
	}
}

func TestSetAuthFileEnableRequiresOwnedMarker(t *testing.T) {
	raw := json.RawMessage(`{"type":"codex","disabled":true}`)
	next, changed, managed, _, errMutate := setAuthFileDisabled(raw, false, authFileMarker{DisabledByPlugin: true})
	if errMutate != nil {
		t.Fatal(errMutate)
	}
	if changed || managed || string(next) != string(raw) {
		t.Fatalf("changed=%v managed=%v next=%s", changed, managed, next)
	}
}

func TestSetAuthFileEnableRemovesOwnedMarker(t *testing.T) {
	raw := json.RawMessage(`{"type":"codex","disabled":true,"codex_quota_guard":{"disabled_by_plugin":true,"auth_id":"auth-1"}}`)
	next, changed, managed, message, errMutate := setAuthFileDisabled(raw, false, authFileMarker{DisabledByPlugin: true, AuthID: "auth-1"})
	if errMutate != nil {
		t.Fatal(errMutate)
	}
	if !changed || !managed || !strings.Contains(message, "enabled") {
		t.Fatalf("changed=%v managed=%v message=%q", changed, managed, message)
	}
	var doc map[string]any
	if errUnmarshal := json.Unmarshal(next, &doc); errUnmarshal != nil {
		t.Fatal(errUnmarshal)
	}
	if disabled, _ := doc["disabled"].(bool); disabled {
		t.Fatalf("disabled = true, want false")
	}
	if _, ok := doc[authFileMarkerKey]; ok {
		t.Fatalf("marker still present: %v", doc[authFileMarkerKey])
	}
}

func TestManualManagementActions(t *testing.T) {
	quotaStore = newQuotaState()
	defer func() { quotaStore = newQuotaState() }()
	respRaw, errHandle := handleManualBlock([]byte(`{"auth_id":"auth-1","duration":"15m","reason":"operator hold"}`))
	if errHandle != nil {
		t.Fatal(errHandle)
	}
	resp := decodeManagementResponse(t, respRaw)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("block status = %d body=%s", resp.StatusCode, resp.Body)
	}
	if got := quotaStore.snapshot(time.Now()).Credentials[0]; got.Status != statusManualBlock {
		t.Fatalf("status = %s, want manual block", got.Status)
	}

	respRaw, errHandle = handleManualUnblock([]byte(`{"auth_id":"auth-1"}`))
	if errHandle != nil {
		t.Fatal(errHandle)
	}
	resp = decodeManagementResponse(t, respRaw)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unblock status = %d body=%s", resp.StatusCode, resp.Body)
	}
	if got := quotaStore.snapshot(time.Now()).Credentials[0]; got.Status == statusManualBlock {
		t.Fatalf("expected unblock, got %+v", got)
	}
}

func decodeManagementResponse(t *testing.T, raw []byte) pluginapi.ManagementResponse {
	t.Helper()
	var env envelope
	if errUnmarshal := json.Unmarshal(raw, &env); errUnmarshal != nil {
		t.Fatal(errUnmarshal)
	}
	if !env.OK {
		t.Fatalf("envelope error = %+v", env.Error)
	}
	var resp pluginapi.ManagementResponse
	if errUnmarshal := json.Unmarshal(env.Result, &resp); errUnmarshal != nil {
		t.Fatal(errUnmarshal)
	}
	return resp
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
