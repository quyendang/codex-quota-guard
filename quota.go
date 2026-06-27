package main

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	statusUsable      = "usable"
	statusNearLimit   = "near-limit"
	statusCooling     = "cooling"
	statusManualBlock = "manual-block"

	windowMinutesPrimary   = 300
	windowMinutesSecondary = 10080

	blockReasonPrimaryThreshold   = "5h quota threshold"
	blockReasonSecondaryThreshold = "weekly quota threshold"
	blockReasonUsageLimit         = "usage limit reached"
	blockReasonFallback429        = "429 usage limit fallback"
)

type quotaState struct {
	mu       sync.Mutex
	records  map[string]*credentialState
	decision schedulerDecision
	events   []stateEvent
}

type credentialState struct {
	AuthID       string       `json:"auth_id"`
	Provider     string       `json:"provider"`
	Label        string       `json:"label,omitempty"`
	Model        string       `json:"model,omitempty"`
	Primary      windowState  `json:"primary"`
	Secondary    windowState  `json:"secondary"`
	BlockedUntil time.Time    `json:"blocked_until,omitempty"`
	BlockReason  string       `json:"block_reason,omitempty"`
	ManualBlock  bool         `json:"manual_block,omitempty"`
	LastSeen     time.Time    `json:"last_seen,omitempty"`
	Last429At    time.Time    `json:"last_429_at,omitempty"`
	LastFailure  usageFailure `json:"last_failure,omitempty"`
}

type windowState struct {
	WindowMinutes int       `json:"window_minutes,omitempty"`
	UsedPercent   float64   `json:"used_percent,omitempty"`
	ResetAt       time.Time `json:"reset_at,omitempty"`
	LastHeaderAt  time.Time `json:"last_header_at,omitempty"`
}

type usageFailure struct {
	StatusCode int    `json:"status_code,omitempty"`
	Body       string `json:"body,omitempty"`
}

type schedulerDecision struct {
	At           time.Time `json:"at,omitempty"`
	Model        string    `json:"model,omitempty"`
	ChosenAuthID string    `json:"chosen_auth_id,omitempty"`
	Filtered     int       `json:"filtered,omitempty"`
	Candidates   int       `json:"candidates,omitempty"`
}

type stateEvent struct {
	At      time.Time `json:"at"`
	AuthID  string    `json:"auth_id,omitempty"`
	Type    string    `json:"type"`
	Message string    `json:"message"`
}

type stateSnapshot struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Credentials []credentialView  `json:"credentials"`
	Summary     stateSummary      `json:"summary"`
	Decision    schedulerDecision `json:"decision,omitempty"`
	Events      []stateEvent      `json:"events,omitempty"`
}

type credentialView struct {
	AuthID       string       `json:"auth_id"`
	Provider     string       `json:"provider"`
	Label        string       `json:"label,omitempty"`
	Model        string       `json:"model,omitempty"`
	Status       string       `json:"status"`
	Primary      windowState  `json:"primary"`
	Secondary    windowState  `json:"secondary"`
	BlockedUntil time.Time    `json:"blocked_until,omitempty"`
	BlockReason  string       `json:"block_reason,omitempty"`
	ManualBlock  bool         `json:"manual_block,omitempty"`
	LastSeen     time.Time    `json:"last_seen,omitempty"`
	Last429At    time.Time    `json:"last_429_at,omitempty"`
	LastFailure  usageFailure `json:"last_failure,omitempty"`
}

type stateSummary struct {
	Total       int `json:"total"`
	Usable      int `json:"usable"`
	NearLimit   int `json:"near_limit"`
	Cooling     int `json:"cooling"`
	ManualBlock int `json:"manual_block"`
	Stale       int `json:"stale"`
}

func newQuotaState() *quotaState {
	return &quotaState{records: make(map[string]*credentialState)}
}

func (s *quotaState) applyUsage(record pluginapi.UsageRecord, cfg pluginConfig, now time.Time) {
	if s == nil {
		return
	}
	authID := strings.TrimSpace(record.AuthID)
	if authID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.ensureLocked(authID)
	state.Provider = providerCodex
	state.Model = record.Model
	state.LastSeen = now
	if label := labelFromUsage(record); label != "" {
		state.Label = label
	}
	if primary, okPrimary := parseWindowHeaders(record.ResponseHeaders, "x-codex-primary-", now); okPrimary {
		state.Primary = primary
	}
	if secondary, okSecondary := parseWindowHeaders(record.ResponseHeaders, "x-codex-secondary-", now); okSecondary {
		state.Secondary = secondary
	}
	if record.Failed {
		state.LastFailure = usageFailure{StatusCode: record.Failure.StatusCode, Body: truncateBody(record.Failure.Body)}
		if record.Failure.StatusCode == http.StatusTooManyRequests {
			state.Last429At = now
		}
	}
	if until, reason, ok := decideBlock(record, state, cfg, now); ok {
		s.blockLocked(state, until, reason, false)
	} else if !record.Failed {
		s.clearAutoBlockIfRecoveredLocked(state, cfg, now)
	}
}

func labelFromUsage(record pluginapi.UsageRecord) string {
	if strings.TrimSpace(record.AuthIndex) != "" {
		return strings.TrimSpace(record.AuthIndex)
	}
	if strings.TrimSpace(record.AuthType) != "" {
		return strings.TrimSpace(record.AuthType)
	}
	return ""
}

func (s *quotaState) ensureLocked(authID string) *credentialState {
	if s.records == nil {
		s.records = make(map[string]*credentialState)
	}
	state := s.records[authID]
	if state == nil {
		state = &credentialState{AuthID: authID, Provider: providerCodex}
		s.records[authID] = state
	}
	return state
}

func decideBlock(record pluginapi.UsageRecord, state *credentialState, cfg pluginConfig, now time.Time) (time.Time, string, bool) {
	threshold := 100 - cfg.RemainingThresholdPercent
	if threshold < 0 {
		threshold = 0
	}
	if record.Failed && record.Failure.StatusCode == http.StatusTooManyRequests && isUsageLimitReached(record.Failure.Body) {
		if reset, reason, ok := resetFromWindows(state, threshold, now); ok {
			if bodyReset := resetFromUsageLimit(record.Failure.Body, now); bodyReset.After(reset) {
				return bodyReset, blockReasonUsageLimit, true
			}
			return reset, reason, true
		}
		if reset := resetFromUsageLimit(record.Failure.Body, now); reset.After(now) {
			return reset, blockReasonUsageLimit, true
		}
		if reset, reason, ok := resetFromUsageHeaders(state, now); ok {
			return reset, reason, true
		}
		return now.Add(cfg.Fallback429Ban), blockReasonFallback429, true
	}
	return resetFromWindows(state, threshold, now)
}

func resetFromWindows(state *credentialState, threshold float64, now time.Time) (time.Time, string, bool) {
	if state == nil {
		return time.Time{}, "", false
	}
	var until time.Time
	reason := ""
	if windowAtThreshold(state.Primary, threshold, now) {
		until = state.Primary.ResetAt
		reason = blockReasonPrimaryThreshold
	}
	if windowAtThreshold(state.Secondary, threshold, now) {
		if until.IsZero() || state.Secondary.ResetAt.After(until) {
			until = state.Secondary.ResetAt
			reason = blockReasonSecondaryThreshold
		}
	}
	if until.After(now) {
		return until, reason, true
	}
	return time.Time{}, "", false
}

func resetFromUsageHeaders(state *credentialState, now time.Time) (time.Time, string, bool) {
	if state == nil {
		return time.Time{}, "", false
	}
	var until time.Time
	reason := ""
	if reset := resetFromKnownWindow(state.Primary, windowMinutesPrimary, now); reset.After(now) {
		until = reset
		reason = blockReasonPrimaryThreshold
	}
	if reset := resetFromKnownWindow(state.Secondary, windowMinutesSecondary, now); reset.After(now) {
		if until.IsZero() || reset.After(until) {
			until = reset
			reason = blockReasonSecondaryThreshold
		}
	}
	if until.After(now) {
		return until, reason, true
	}
	return time.Time{}, "", false
}

func resetFromKnownWindow(window windowState, minutes int, now time.Time) time.Time {
	if window.WindowMinutes != minutes || window.ResetAt.IsZero() || !window.ResetAt.After(now) {
		return time.Time{}
	}
	return window.ResetAt
}

func windowAtThreshold(window windowState, threshold float64, now time.Time) bool {
	if window.UsedPercent <= 0 || window.ResetAt.IsZero() || !window.ResetAt.After(now) {
		return false
	}
	return window.UsedPercent >= threshold
}

func parseWindowHeaders(headers http.Header, prefix string, now time.Time) (windowState, bool) {
	if headers == nil {
		return windowState{}, false
	}
	minutes, okMinutes := headerInt(headers, prefix+"window-minutes")
	used, okUsed := headerFloat(headers, prefix+"used-percent")
	resetAt, okReset := headerUnixTime(headers, prefix+"reset-at")
	if !okMinutes && !okUsed && !okReset {
		return windowState{}, false
	}
	return windowState{
		WindowMinutes: minutes,
		UsedPercent:   used,
		ResetAt:       resetAt,
		LastHeaderAt:  now,
	}, true
}

func headerInt(headers http.Header, key string) (int, bool) {
	raw := strings.TrimSpace(headers.Get(key))
	if raw == "" {
		return 0, false
	}
	value, errParse := strconv.Atoi(raw)
	if errParse != nil {
		return 0, false
	}
	return value, true
}

func headerFloat(headers http.Header, key string) (float64, bool) {
	raw := strings.TrimSpace(headers.Get(key))
	if raw == "" {
		return 0, false
	}
	value, errParse := strconv.ParseFloat(raw, 64)
	if errParse != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func headerUnixTime(headers http.Header, key string) (time.Time, bool) {
	raw := strings.TrimSpace(headers.Get(key))
	if raw == "" {
		return time.Time{}, false
	}
	value, errParse := strconv.ParseInt(raw, 10, 64)
	if errParse != nil || value <= 0 {
		return time.Time{}, false
	}
	return time.Unix(value, 0), true
}

func isUsageLimitReached(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}
	var decoded map[string]any
	if errUnmarshal := json.Unmarshal([]byte(body), &decoded); errUnmarshal != nil {
		return strings.Contains(strings.ToLower(body), "usage_limit_reached")
	}
	return nestedString(decoded, "error", "type") == "usage_limit_reached" || nestedString(decoded, "type") == "usage_limit_reached"
}

func resetFromUsageLimit(body string, now time.Time) time.Time {
	var decoded map[string]any
	if errUnmarshal := json.Unmarshal([]byte(body), &decoded); errUnmarshal != nil {
		return time.Time{}
	}
	if resetAt := nestedFloat(decoded, "error", "resets_at"); resetAt > 0 {
		return time.Unix(int64(resetAt), 0)
	}
	if resetAt := nestedFloat(decoded, "resets_at"); resetAt > 0 {
		return time.Unix(int64(resetAt), 0)
	}
	if resetsIn := nestedFloat(decoded, "error", "resets_in_seconds"); resetsIn > 0 {
		return now.Add(time.Duration(resetsIn) * time.Second)
	}
	if resetsIn := nestedFloat(decoded, "resets_in_seconds"); resetsIn > 0 {
		return now.Add(time.Duration(resetsIn) * time.Second)
	}
	return time.Time{}
}

func nestedString(root map[string]any, path ...string) string {
	var cur any = root
	for _, part := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[part]
	}
	value, _ := cur.(string)
	return strings.TrimSpace(value)
}

func nestedFloat(root map[string]any, path ...string) float64 {
	var cur any = root
	for _, part := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0
		}
		cur = m[part]
	}
	switch value := cur.(type) {
	case float64:
		return value
	case int64:
		return float64(value)
	case json.Number:
		parsed, _ := value.Float64()
		return parsed
	default:
		return 0
	}
}

func (s *quotaState) blockLocked(state *credentialState, until time.Time, reason string, manual bool) {
	if state == nil || until.IsZero() {
		return
	}
	changed := state.BlockedUntil.IsZero() || until.After(state.BlockedUntil) || state.BlockReason != reason || state.ManualBlock != manual
	state.BlockedUntil = until
	state.BlockReason = reason
	state.ManualBlock = manual
	if changed {
		s.addEventLocked(state.AuthID, "block", reason)
		logStateChange("soft-blocked credential", "auth_id", state.AuthID, "reason", reason, "blocked_until", until.Format(time.RFC3339))
	}
}

func (s *quotaState) clearAutoBlockIfRecoveredLocked(state *credentialState, cfg pluginConfig, now time.Time) {
	if state == nil || state.ManualBlock || state.BlockedUntil.IsZero() {
		return
	}
	if state.BlockedUntil.Before(now) || state.BlockedUntil.Equal(now) {
		return
	}
	threshold := 100 - cfg.RemainingThresholdPercent
	if windowAtThreshold(state.Primary, threshold, now) || windowAtThreshold(state.Secondary, threshold, now) {
		return
	}
	state.BlockedUntil = time.Time{}
	state.BlockReason = ""
	s.addEventLocked(state.AuthID, "unblock", "quota headers recovered")
	logStateChange("cleared recovered credential block", "auth_id", state.AuthID)
}

func (s *quotaState) availableCandidates(candidates []pluginapi.SchedulerAuthCandidate, now time.Time) ([]pluginapi.SchedulerAuthCandidate, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	available := make([]pluginapi.SchedulerAuthCandidate, 0, len(candidates))
	filtered := 0
	for _, candidate := range candidates {
		if !strings.EqualFold(candidate.Provider, providerCodex) {
			available = append(available, candidate)
			continue
		}
		state := s.records[candidate.ID]
		if state == nil {
			available = append(available, candidate)
			continue
		}
		if state.Provider == "" {
			state.Provider = providerCodex
		}
		if state.Label == "" {
			state.Label = candidateLabel(candidate)
		}
		if state.BlockedUntil.After(now) {
			filtered++
			continue
		}
		if !state.BlockedUntil.IsZero() {
			s.addEventLocked(candidate.ID, "unblock", "reset time passed")
			state.BlockedUntil = time.Time{}
			state.BlockReason = ""
			state.ManualBlock = false
			logStateChange("auto-unblocked credential", "auth_id", candidate.ID)
		}
		available = append(available, candidate)
	}
	return available, filtered
}

func candidateLabel(candidate pluginapi.SchedulerAuthCandidate) string {
	for _, key := range []string{"email", "label", "account", "auth_index"} {
		if value := strings.TrimSpace(candidate.Attributes[key]); value != "" {
			return value
		}
		if raw, ok := candidate.Metadata[key].(string); ok && strings.TrimSpace(raw) != "" {
			return strings.TrimSpace(raw)
		}
	}
	return ""
}

func (s *quotaState) recordDecision(decision schedulerDecision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decision = decision
}

func (s *quotaState) manualBlock(authID string, until time.Time, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.ensureLocked(authID)
	state.Provider = providerCodex
	s.blockLocked(state, until, reason, true)
}

func (s *quotaState) unblock(authID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.records[authID]
	if state == nil {
		return
	}
	state.BlockedUntil = time.Time{}
	state.BlockReason = ""
	state.ManualBlock = false
	s.addEventLocked(authID, "unblock", "manual unblock")
	logStateChange("manually unblocked credential", "auth_id", authID)
}

func (s *quotaState) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = make(map[string]*credentialState)
	s.decision = schedulerDecision{}
	s.events = nil
	logStateChange("cleared quota state")
}

func (s *quotaState) snapshot(now time.Time) stateSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	views := make([]credentialView, 0, len(s.records))
	summary := stateSummary{}
	for _, state := range s.records {
		view := credentialView{
			AuthID:       state.AuthID,
			Provider:     state.Provider,
			Label:        state.Label,
			Model:        state.Model,
			Status:       stateStatus(state, now),
			Primary:      state.Primary,
			Secondary:    state.Secondary,
			BlockedUntil: state.BlockedUntil,
			BlockReason:  state.BlockReason,
			ManualBlock:  state.ManualBlock,
			LastSeen:     state.LastSeen,
			Last429At:    state.Last429At,
			LastFailure:  state.LastFailure,
		}
		views = append(views, view)
		summary.Total++
		switch view.Status {
		case statusUsable:
			summary.Usable++
		case statusNearLimit:
			summary.NearLimit++
		case statusCooling:
			summary.Cooling++
		case statusManualBlock:
			summary.ManualBlock++
		}
		if state.LastSeen.IsZero() {
			summary.Stale++
		}
	}
	views = sortedCredentialStates(views)
	events := append([]stateEvent(nil), s.events...)
	return stateSnapshot{
		GeneratedAt: now,
		Credentials: views,
		Summary:     summary,
		Decision:    s.decision,
		Events:      events,
	}
}

func stateStatus(state *credentialState, now time.Time) string {
	if state == nil {
		return statusUsable
	}
	if state.BlockedUntil.After(now) {
		if state.ManualBlock {
			return statusManualBlock
		}
		return statusCooling
	}
	if state.Primary.UsedPercent >= 90 || state.Secondary.UsedPercent >= 90 {
		return statusNearLimit
	}
	return statusUsable
}

func (s *quotaState) addEventLocked(authID, eventType, message string) {
	s.events = append(s.events, stateEvent{
		At:      time.Now(),
		AuthID:  authID,
		Type:    eventType,
		Message: message,
	})
	if len(s.events) > 50 {
		s.events = s.events[len(s.events)-50:]
	}
}

func truncateBody(body string) string {
	body = strings.TrimSpace(body)
	if len(body) <= 300 {
		return body
	}
	return body[:300]
}
