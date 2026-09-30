const { api, shortID, setMessage, withBusy } = window.LockFlow;
const form = document.querySelector("#plan-form");
const message = document.querySelector("#plan-message");

// /healthz answers 503 while any chamber is offline or unknown, so read the
// body regardless of the HTTP status.
const fetchHealth = async () => {
  const response = await fetch("/healthz");
  return response.json().catch(() => ({}));
};

const STATUS_CLASS = { online: "", offline: "critical", unknown: "warning" };
const HEALTH_LABEL = {
  ok: ["Devices online", ""],
  offline: ["Devices offline", "critical"],
  unknown: ["Awaiting telemetry", "warning"],
};

function deviceReading(entry) {
  const observation = entry.observation;
  if (!observation) return ["-", "never"];
  let reading = observation.status;
  if (observation.position) reading = observation.position;
  else if (observation.opening_pct !== null && observation.opening_pct !== undefined) {
    reading = `${observation.opening_pct}%`;
  }
  return [reading, new Date(observation.observed_at).toLocaleTimeString()];
}

async function refresh() {
  const [plans, chambers, health, devices] = await Promise.all([
    api("/api/plans"), api("/api/chambers"), fetchHealth(), api("/api/telemetry/devices"),
  ]);
  const healthBadge = document.querySelector("#health");
  const [label, severity] = HEALTH_LABEL[health.status] || ["Attention required", "critical"];
  healthBadge.textContent = label;
  healthBadge.className = `status ${severity}`.trim();
  const rows = Object.entries(plans).flatMap(([chamber, items]) => items.map((plan) => `<tr><td>${chamber}</td><td>${shortID(plan.id)}</td><td>${plan.direction}</td><td>${plan.vessels.length}</td><td>${new Date(plan.created_at).toLocaleTimeString()}</td></tr>`));
  document.querySelector("#plans").innerHTML = rows.join("") || '<tr><td colspan="5" class="muted">No active plans</td></tr>';
  const deviceRows = devices.map((entry) => {
    const [reading, observed] = deviceReading(entry);
    return `<tr><td>${entry.health.chamber_id}</td><td>${entry.health.device}</td><td><span class="status ${STATUS_CLASS[entry.health.status] || "warning"}">${entry.health.status}</span></td><td>${reading}</td><td>${observed}</td></tr>`;
  });
  document.querySelector("#devices").innerHTML = deviceRows.join("") || '<tr><td colspan="5" class="muted">No devices registered</td></tr>';
  const current = chambers.find((item) => item.cycle);
  document.querySelector("#metric-chamber").textContent = current?.chamber_id || "Idle";
  document.querySelector("#metric-stage").textContent = current?.cycle?.stage || "-";
  document.querySelector("#metric-cycle").textContent = shortID(current?.cycle?.id);
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = form.querySelector("button");
  await withBusy(button, async () => {
    try {
      const result = await api("/api/plans", { method: "POST", body: JSON.stringify({
        chamber_id: document.querySelector("#chamber").value,
        direction: document.querySelector("#direction").value,
        vessels: [{ name: document.querySelector("#vessel").value, call_sign: document.querySelector("#call-sign").value, length_m: Number(document.querySelector("#length").value) }],
      }) });
      setMessage(message, `Plan ${shortID(result.plan.id)} admitted to ${result.plan.chamber_id}.`);
      await refresh();
    } catch (error) { setMessage(message, error.message); }
  });
});

refresh().catch((error) => setMessage(message, error.message));
setInterval(() => refresh().catch(() => {}), 5000);
