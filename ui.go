package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"
)

func renderStatusPage(snapshot stateSnapshot, cfg pluginConfig) []byte {
	stateJSON, _ := json.Marshal(snapshot)
	stateScript := strings.ReplaceAll(string(stateJSON), "</", "<\\/")
	var out bytes.Buffer
	out.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	out.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	out.WriteString(`<title>Codex Quota Guard</title>`)
	out.WriteString(`<style>`)
	out.WriteString(statusPageCSS())
	out.WriteString(`</style></head><body>`)
	out.WriteString(`<main class="shell">`)
	out.WriteString(`<header class="hero">`)
	out.WriteString(`<div><p class="eyebrow">Codex quota guard</p><h1>Quota pool</h1>`)
	out.WriteString(`<p class="subhead">Codex credentials, reset windows, and soft blocks in one quiet control surface.</p></div>`)
	out.WriteString(`<dl class="summary" id="summary">`)
	writeSummaryItem(&out, "Usable", snapshot.Summary.Usable)
	writeSummaryItem(&out, "Cooling", snapshot.Summary.Cooling)
	writeSummaryItem(&out, "Near limit", snapshot.Summary.NearLimit)
	writeSummaryItem(&out, "Manual", snapshot.Summary.ManualBlock)
	out.WriteString(`</dl></header>`)
	out.WriteString(`<section class="rail" aria-label="Quota reset rail"><div class="rail-head"><span>5h session rail</span><span>weekly rail</span></div><div id="quota-rail" class="rail-grid"></div></section>`)
	out.WriteString(`<section class="workspace">`)
	out.WriteString(`<div class="panel table-panel"><div class="panel-head"><h2>Credentials</h2><span id="generated-at" class="muted"></span></div>`)
	out.WriteString(`<div class="table-wrap"><table><thead><tr><th>Account</th><th>Status</th><th>5h</th><th>Weekly</th><th>Reset</th></tr></thead><tbody id="credential-rows">`)
	for _, item := range snapshot.Credentials {
		writeCredentialRow(&out, item)
	}
	out.WriteString(`</tbody></table></div></div>`)
	out.WriteString(`<aside class="panel detail-panel" id="detail"><div class="panel-head"><h2>Selected account</h2><span class="muted">No selection</span></div><p class="empty">Select a credential to inspect quota windows and actions.</p></aside>`)
	out.WriteString(`</section>`)
	out.WriteString(`<section class="lower">`)
	out.WriteString(`<div class="panel"><div class="panel-head"><h2>Configuration</h2></div>`)
	out.WriteString(`<dl class="config-grid">`)
	writeConfig(&out, "Remaining threshold", fmt.Sprintf("%.0f%%", cfg.RemainingThresholdPercent))
	writeConfig(&out, "Fallback 429 ban", cfg.Fallback429Ban.String())
	writeConfig(&out, "Manual block duration", cfg.ManualBlockDuration.String())
	out.WriteString(`</dl></div>`)
	out.WriteString(`<div class="panel"><div class="panel-head"><h2>Event trail</h2></div><ul class="events" id="events">`)
	writeEvents(&out, snapshot.Events)
	out.WriteString(`</ul></div>`)
	out.WriteString(`</section>`)
	out.WriteString(`<script id="initial-state" type="application/json">`)
	out.WriteString(stateScript)
	out.WriteString(`</script><script>`)
	out.WriteString(statusPageJS())
	out.WriteString(`</script></main></body></html>`)
	return out.Bytes()
}

func statusPageCSS() string {
	return `
:root {
  --bg: #f7f8fa;
  --surface: #ffffff;
  --surface-soft: #fbfcfd;
  --text: #111827;
  --muted: #6b7280;
  --line: #e5e7eb;
  --line-strong: #d1d5db;
  --blue: #2563eb;
  --green: #16a34a;
  --amber: #d97706;
  --red: #dc2626;
  --shadow: 0 1px 2px rgba(17, 24, 39, .04);
}
* { box-sizing: border-box; }
html { background: var(--bg); }
body {
  margin: 0;
  background: var(--bg);
  color: var(--text);
  font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  -webkit-font-smoothing: antialiased;
}
button, input { font: inherit; }
.shell { width: min(1360px, calc(100vw - 40px)); margin: 0 auto; padding: 28px 0 36px; }
.hero {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(360px, auto);
  gap: 24px;
  align-items: end;
  padding: 4px 0 18px;
}
.eyebrow, .muted, th, .status, .window-label, .config-grid dt, .account {
  font-family: "SFMono-Regular", Consolas, ui-monospace, monospace;
}
.eyebrow {
  margin: 0 0 8px;
  color: var(--blue);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: .12em;
  text-transform: uppercase;
}
h1 {
  margin: 0;
  font-size: clamp(32px, 4vw, 56px);
  font-weight: 760;
  line-height: 1;
  letter-spacing: 0;
}
.subhead { max-width: 640px; margin: 12px 0 0; color: var(--muted); font-size: 15px; line-height: 1.55; }
.summary { display: grid; grid-template-columns: repeat(4, minmax(78px, 1fr)); gap: 10px; margin: 0; }
.summary div {
  min-height: 82px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 13px 14px;
  box-shadow: var(--shadow);
}
.summary dt { color: var(--muted); font-size: 11px; font-weight: 650; text-transform: uppercase; letter-spacing: .04em; }
.summary dd { margin: 8px 0 0; font: 720 30px/1 "SFMono-Regular", Consolas, ui-monospace, monospace; letter-spacing: 0; }
.rail {
  margin: 10px 0 18px;
  padding: 14px 16px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 8px;
  box-shadow: var(--shadow);
}
.rail-head {
  display: flex;
  justify-content: space-between;
  color: var(--muted);
  font: 11px "SFMono-Regular", Consolas, ui-monospace, monospace;
  letter-spacing: .02em;
  margin-bottom: 12px;
}
.rail-grid { display: grid; gap: 9px; min-height: 64px; }
.rail-row { display: grid; grid-template-columns: 168px 1fr; align-items: center; gap: 14px; }
.rail-label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: #374151; font: 12px "SFMono-Regular", Consolas, ui-monospace, monospace; }
.track { position: relative; height: 6px; background: #eef2f7; border-radius: 999px; overflow: visible; }
.stop { position: absolute; top: -4px; width: 4px; height: 14px; background: var(--green); border-radius: 999px; box-shadow: 0 0 0 2px var(--surface); }
.stop.cooling { background: var(--red); }
.stop.manual-block { background: var(--amber); }
.stop.near-limit { background: var(--amber); }
.workspace { display: grid; grid-template-columns: minmax(0, 1.55fr) minmax(320px, .55fr); gap: 16px; }
.lower { display: grid; grid-template-columns: minmax(300px, .72fr) minmax(0, 1.28fr); gap: 16px; margin-top: 16px; }
.panel {
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 8px;
  box-shadow: var(--shadow);
  overflow: hidden;
}
.panel-head { display: flex; justify-content: space-between; align-items: center; gap: 16px; min-height: 50px; padding: 14px 16px; border-bottom: 1px solid var(--line); }
h2 { margin: 0; font-size: 14px; font-weight: 720; letter-spacing: 0; }
.muted { color: var(--muted); font-size: 12px; }
.table-wrap { overflow: auto; }
table { width: 100%; border-collapse: collapse; min-width: 780px; }
th, td { padding: 13px 16px; border-bottom: 1px solid var(--line); text-align: left; font-size: 14px; }
th { color: var(--muted); font-size: 11px; font-weight: 700; letter-spacing: .04em; text-transform: uppercase; }
tr { cursor: pointer; }
tr:hover, tr.selected { background: var(--surface-soft); }
tr.selected { box-shadow: inset 3px 0 0 var(--blue); }
.account { color: var(--text); font-size: 12px; font-weight: 650; }
.status {
  display: inline-flex;
  align-items: center;
  min-height: 24px;
  padding: 4px 8px;
  border: 1px solid var(--line);
  border-radius: 999px;
  background: #f9fafb;
  font-size: 11px;
  font-weight: 700;
}
.status.usable { color: var(--green); background: #f0fdf4; border-color: #bbf7d0; }
.status.near-limit { color: var(--amber); background: #fffbeb; border-color: #fde68a; }
.status.cooling, .status.manual-block { color: var(--red); background: #fef2f2; border-color: #fecaca; }
.meter { width: 118px; height: 6px; background: #eef2f7; border-radius: 999px; overflow: hidden; }
.meter span { display: block; height: 100%; background: var(--green); border-radius: inherit; }
.meter.warn span { background: var(--amber); }
.meter.danger span { background: var(--red); }
.detail-body { padding: 16px; }
.empty { padding: 16px; color: var(--muted); line-height: 1.5; }
.windows { display: grid; gap: 0; margin: 16px 0; border-top: 1px solid var(--line); }
.window-card { padding: 14px 0; border-bottom: 1px solid var(--line); }
.window-label { display: flex; justify-content: space-between; color: var(--muted); font-size: 11px; font-weight: 700; letter-spacing: .04em; text-transform: uppercase; margin-bottom: 10px; }
.actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 14px; }
.actions button {
  border: 1px solid var(--text);
  border-radius: 7px;
  background: var(--text);
  color: white;
  padding: 8px 11px;
  font-size: 13px;
  font-weight: 650;
  cursor: pointer;
}
.actions button.secondary { background: var(--surface); color: var(--text); border-color: var(--line-strong); }
.actions button:hover { transform: translateY(-1px); }
.actions button:focus-visible, tr:focus-visible { outline: 3px solid rgba(37, 99, 235, .18); outline-offset: 2px; }
.config-grid { display: grid; grid-template-columns: max-content 1fr; gap: 11px 18px; padding: 16px; margin: 0; }
.config-grid dt { color: var(--muted); font-size: 11px; font-weight: 700; letter-spacing: .04em; text-transform: uppercase; }
.config-grid dd { margin: 0; font-family: "SFMono-Regular", Consolas, ui-monospace, monospace; font-size: 12px; }
.events { margin: 0; padding: 8px 16px 16px; list-style: none; max-height: 240px; overflow: auto; }
.events li { padding: 10px 0; border-bottom: 1px solid var(--line); font-size: 13px; line-height: 1.45; }
@media (max-width: 900px) {
  .hero, .workspace, .lower { grid-template-columns: 1fr; }
  .summary { grid-template-columns: repeat(2, 1fr); }
  .shell { width: min(100vw - 20px, 1360px); padding-top: 18px; }
  .rail-row { grid-template-columns: 1fr; gap: 7px; }
}
@media (prefers-reduced-motion: no-preference) {
  .pulse { animation: pulse .48s ease-out; }
  @keyframes pulse { from { box-shadow: 0 0 0 0 rgba(37, 99, 235, .18); } to { box-shadow: 0 0 0 14px rgba(37, 99, 235, 0); } }
}`
}

func statusPageJS() string {
	return `
const initial = JSON.parse(document.getElementById('initial-state').textContent || '{}');
let state = initial;
let selected = state.credentials && state.credentials[0] ? state.credentials[0].auth_id : '';

function fmtDate(value) {
  if (!value || value === '0001-01-01T00:00:00Z') return 'none';
  return new Date(value).toLocaleString();
}
function pct(value) {
  const n = Number(value || 0);
  return Math.max(0, Math.min(100, n));
}
function meter(value) {
  const p = pct(value);
  const tone = p >= 95 ? 'danger' : p >= 90 ? 'warn' : '';
  return '<div class="meter '+tone+'"><span style="width:'+p+'%"></span></div>';
}
function esc(value) {
  return String(value || '').replace(/[&<>"']/g, ch => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[ch]));
}
function label(item) {
  return item.label || item.auth_id || 'unknown';
}
function renderSummary() {
  const s = state.summary || {};
  document.getElementById('summary').innerHTML =
    summaryItem('Usable', s.usable || 0) + summaryItem('Cooling', s.cooling || 0) +
    summaryItem('Near limit', s.near_limit || 0) + summaryItem('Manual', s.manual_block || 0);
  document.getElementById('generated-at').textContent = 'Updated ' + fmtDate(state.generated_at);
}
function summaryItem(k, v) { return '<div><dt>'+esc(k)+'</dt><dd>'+esc(v)+'</dd></div>'; }
function renderRows() {
  const rows = document.getElementById('credential-rows');
  const items = state.credentials || [];
  if (!items.length) {
    rows.innerHTML = '<tr><td colspan="5" class="muted">No Codex quota state yet. Send traffic through Codex to populate this panel.</td></tr>';
    return;
  }
  rows.innerHTML = items.map(item => {
    const reset = item.blocked_until && item.blocked_until !== '0001-01-01T00:00:00Z' ? item.blocked_until : (item.primary.reset_at || item.secondary.reset_at);
    return '<tr tabindex="0" data-auth="'+esc(item.auth_id)+'" class="'+(item.auth_id === selected ? 'selected' : '')+'">' +
      '<td><div class="account">'+esc(label(item))+'</div><div class="muted">'+esc(item.auth_id)+'</div></td>' +
      '<td><span class="status '+esc(item.status)+'">'+esc(item.status)+'</span></td>' +
      '<td>'+meter(item.primary.used_percent)+'<div class="muted">'+esc(item.primary.used_percent || 0)+'%</div></td>' +
      '<td>'+meter(item.secondary.used_percent)+'<div class="muted">'+esc(item.secondary.used_percent || 0)+'%</div></td>' +
      '<td class="muted">'+esc(fmtDate(reset))+'</td></tr>';
  }).join('');
  rows.querySelectorAll('tr[data-auth]').forEach(row => {
    row.addEventListener('click', () => { selected = row.dataset.auth; render(); });
    row.addEventListener('keydown', ev => { if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); selected = row.dataset.auth; render(); } });
  });
}
function renderDetail() {
  const box = document.getElementById('detail');
  const item = (state.credentials || []).find(x => x.auth_id === selected);
  if (!item) {
    box.innerHTML = '<div class="panel-head"><h2>Selected account</h2><span class="muted">No selection</span></div><p class="empty">Select a credential to inspect quota windows and actions.</p>';
    return;
  }
  box.innerHTML = '<div class="panel-head"><h2>'+esc(label(item))+'</h2><span class="status '+esc(item.status)+'">'+esc(item.status)+'</span></div>' +
    '<div class="detail-body"><div class="account">'+esc(item.auth_id)+'</div><p class="muted">'+esc(item.block_reason || 'No active soft block')+'</p>' +
    '<div class="windows">'+windowCard('5h window', item.primary)+windowCard('Weekly window', item.secondary)+'</div>' +
    '<div class="actions"><button onclick="unblockSelected()">Unblock</button><button class="secondary" onclick="blockSelected()">Block 1h</button><button class="secondary" onclick="refreshState()">Refresh</button></div></div>';
}
function windowCard(title, win) {
  return '<div class="window-card"><div class="window-label"><span>'+esc(title)+'</span><span>'+esc(win.window_minutes || '')+'m</span></div>' +
    meter(win.used_percent) + '<p class="muted">Reset at '+esc(fmtDate(win.reset_at))+'</p></div>';
}
function renderRail() {
  const rail = document.getElementById('quota-rail');
  const items = state.credentials || [];
  rail.innerHTML = items.slice(0, 14).map(item => {
    const primary = pct(item.primary.used_percent);
    const secondary = pct(item.secondary.used_percent);
    return '<div class="rail-row"><div class="rail-label">'+esc(label(item))+'</div><div><div class="track"><span class="stop '+esc(item.status)+'" style="left:'+primary+'%"></span></div><div class="track" style="margin-top:6px"><span class="stop '+esc(item.status)+'" style="left:'+secondary+'%"></span></div></div></div>';
  }).join('') || '<div class="muted">Quota rail will appear after the first Codex usage record.</div>';
}
function renderEvents() {
  const events = state.events || [];
  document.getElementById('events').innerHTML = events.slice().reverse().map(ev =>
    '<li><span class="muted">'+esc(fmtDate(ev.at))+'</span> '+esc(ev.type)+' '+esc(ev.auth_id)+'<br>'+esc(ev.message)+'</li>'
  ).join('') || '<li class="muted">No plugin events yet.</li>';
}
function render() { renderSummary(); renderRows(); renderDetail(); renderRail(); renderEvents(); }
async function refreshState() {
  try {
    const resp = await fetch(window.location.pathname + '?format=json', { credentials: 'same-origin' });
    if (resp.ok) { state = await resp.json(); render(); document.querySelector('.panel')?.classList.add('pulse'); }
  } catch (_) {}
}
async function postAction(path, body) {
  await fetch('/v0/management/codex-quota-guard/' + path, {
    method: 'POST', credentials: 'same-origin', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body)
  });
  await refreshState();
}
function unblockSelected() { if (selected) postAction('unblock', { auth_id: selected }); }
function blockSelected() { if (selected) postAction('block', { auth_id: selected, duration: '1h', reason: 'manual block' }); }
render();
setInterval(refreshState, 30000);`
}

func writeSummaryItem(out *bytes.Buffer, label string, value int) {
	out.WriteString(`<div><dt>`)
	out.WriteString(html.EscapeString(label))
	out.WriteString(`</dt><dd>`)
	out.WriteString(html.EscapeString(fmt.Sprintf("%d", value)))
	out.WriteString(`</dd></div>`)
}

func writeCredentialRow(out *bytes.Buffer, item credentialView) {
	reset := item.BlockedUntil
	if reset.IsZero() {
		reset = item.Primary.ResetAt
	}
	if reset.IsZero() {
		reset = item.Secondary.ResetAt
	}
	out.WriteString(`<tr data-auth="`)
	out.WriteString(html.EscapeString(item.AuthID))
	out.WriteString(`"><td><div class="account">`)
	out.WriteString(html.EscapeString(displayLabel(item)))
	out.WriteString(`</div><div class="muted">`)
	out.WriteString(html.EscapeString(item.AuthID))
	out.WriteString(`</div></td><td><span class="status `)
	out.WriteString(html.EscapeString(item.Status))
	out.WriteString(`">`)
	out.WriteString(html.EscapeString(item.Status))
	out.WriteString(`</span></td><td>`)
	writeMeter(out, item.Primary.UsedPercent)
	out.WriteString(`</td><td>`)
	writeMeter(out, item.Secondary.UsedPercent)
	out.WriteString(`</td><td class="muted">`)
	out.WriteString(html.EscapeString(formatTime(reset)))
	out.WriteString(`</td></tr>`)
}

func writeMeter(out *bytes.Buffer, value float64) {
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	tone := ""
	if value >= 95 {
		tone = " danger"
	} else if value >= 90 {
		tone = " warn"
	}
	out.WriteString(`<div class="meter`)
	out.WriteString(tone)
	out.WriteString(`"><span style="width:`)
	out.WriteString(html.EscapeString(fmt.Sprintf("%.0f", value)))
	out.WriteString(`%"></span></div><div class="muted">`)
	out.WriteString(html.EscapeString(fmt.Sprintf("%.0f%%", value)))
	out.WriteString(`</div>`)
}

func writeConfig(out *bytes.Buffer, key, value string) {
	out.WriteString(`<dt>`)
	out.WriteString(html.EscapeString(key))
	out.WriteString(`</dt><dd>`)
	out.WriteString(html.EscapeString(value))
	out.WriteString(`</dd>`)
}

func writeEvents(out *bytes.Buffer, events []stateEvent) {
	if len(events) == 0 {
		out.WriteString(`<li class="muted">No plugin events yet.</li>`)
		return
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		out.WriteString(`<li><span class="muted">`)
		out.WriteString(html.EscapeString(formatTime(event.At)))
		out.WriteString(`</span> `)
		out.WriteString(html.EscapeString(event.Type))
		out.WriteString(` `)
		out.WriteString(html.EscapeString(event.AuthID))
		out.WriteString(`<br>`)
		out.WriteString(html.EscapeString(event.Message))
		out.WriteString(`</li>`)
	}
}

func displayLabel(item credentialView) string {
	if strings.TrimSpace(item.Label) != "" {
		return strings.TrimSpace(item.Label)
	}
	return item.AuthID
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "none"
	}
	return value.Format(time.RFC3339)
}
