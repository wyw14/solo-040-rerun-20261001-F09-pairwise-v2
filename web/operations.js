const { api, shortID, setMessage, withBusy } = window.LockFlow;
const form = document.querySelector("#plan-form");
const message = document.querySelector("#plan-message");

const toLocalInput = (iso) => {
  if (!iso) return "";
  const date = new Date(iso);
  const pad = (value) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
};
const fromLocalInput = (value) => (value ? new Date(value).toISOString() : null);
const showTime = (iso) => (iso ? new Date(iso).toLocaleTimeString() : "-");

async function refresh() {
  const [plans, chambers, health] = await Promise.all([api("/api/plans"), api("/api/chambers"), api("/healthz")]);
  document.querySelector("#health").textContent = health.status === "ok" ? "Devices online" : "Attention required";
  const rows = Object.entries(plans).flatMap(([chamber, items]) => items.map((plan) => {
    const basis = plan.eta ? `ETA ${showTime(plan.eta)}` : `created ${showTime(plan.created_at)}`;
    return `<tr data-plan="${plan.id}"><td>${chamber}</td><td>${shortID(plan.id)}</td><td>${plan.direction}</td><td>${plan.vessels.length}</td>` +
      `<td>P${plan.priority}</td><td title="${basis}">${showTime(plan.eta)}</td><td>${showTime(plan.created_at)}</td>` +
      `<td><div class="adjust-row"><input type="number" class="adjust-priority" min="0" max="9" value="${plan.priority}" aria-label="Priority">` +
      `<input type="datetime-local" class="adjust-eta" value="${toLocalInput(plan.eta)}" aria-label="Expected arrival">` +
      `<button type="button" class="adjust-apply">Apply</button></div></td></tr>`;
  }));
  document.querySelector("#plans").innerHTML = rows.join("") || '<tr><td colspan="8" class="muted">No active plans</td></tr>';
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
        priority: Number(document.querySelector("#priority").value || 0),
        eta: fromLocalInput(document.querySelector("#eta").value),
      }) });
      setMessage(message, `Plan ${shortID(result.plan.id)} admitted to ${result.plan.chamber_id} (P${result.plan.priority}).`);
      await refresh();
    } catch (error) { setMessage(message, error.message); }
  });
});

document.querySelector("#plans").addEventListener("click", async (event) => {
  const button = event.target.closest(".adjust-apply");
  if (!button) return;
  const row = button.closest("tr");
  const planID = row.dataset.plan;
  await withBusy(button, async () => {
    try {
      await api(`/api/plans/${planID}/adjust`, { method: "POST", body: JSON.stringify({
        priority: Number(row.querySelector(".adjust-priority").value || 0),
        eta: fromLocalInput(row.querySelector(".adjust-eta").value),
      }) });
      setMessage(message, `Plan ${shortID(planID)} adjusted; queue re-sorted by priority, expected arrival, then arrival order.`);
      await refresh();
    } catch (error) { setMessage(message, error.message); }
  });
});

refresh().catch((error) => setMessage(message, error.message));
setInterval(() => refresh().catch(() => {}), 5000);
