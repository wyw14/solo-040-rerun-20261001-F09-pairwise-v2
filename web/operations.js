const { api, shortID, setMessage, withBusy } = window.LockFlow;
const form = document.querySelector("#plan-form");
const message = document.querySelector("#plan-message");

const PRIORITIES = { 1: "High", 2: "Normal", 3: "Low" };
const priorityLabel = (value) => `${value} · ${PRIORITIES[value] || "Normal"}`;
const formatETA = (value) => {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) return "-";
  return date.toLocaleString();
};
const pad = (n) => String(n).padStart(2, "0");
const toLocalInput = (value) => {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime()) || d.getUTCFullYear() <= 1) return "";
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
};

async function refresh() {
  const [plans, chambers, health] = await Promise.all([api("/api/plans"), api("/api/chambers"), api("/healthz")]);
  document.querySelector("#health").textContent = health.status === "ok" ? "Devices online" : "Attention required";

  // A plan with any released vessel has started moving and is frozen.
  const releasedPlans = new Set();
  const queueByChamber = {};
  for (const chamber of chambers) {
    queueByChamber[chamber.chamber_id] = chamber.queue || [];
    for (const entry of chamber.queue || []) {
      if (entry.released) releasedPlans.add(entry.plan_id);
    }
  }

  const rows = Object.entries(plans).flatMap(([chamber, items]) => items.map((plan) => {
    const released = releasedPlans.has(plan.id);
    const status = released
      ? '<span class="badge badge-released">released · locked</span>'
      : '<span class="badge badge-waiting">waiting</span>';
    const controls = released ? '<span class="muted small">cannot reorder</span>' : `
      <form class="schedule-form" data-plan="${plan.id}">
        <select name="priority" title="Priority">
          <option value="1" ${plan.priority === 1 ? "selected" : ""}>1 · High</option>
          <option value="2" ${plan.priority === 2 ? "selected" : ""}>2 · Normal</option>
          <option value="3" ${plan.priority === 3 ? "selected" : ""}>3 · Low</option>
        </select>
        <input name="eta" type="datetime-local" value="${toLocalInput(plan.eta)}" title="Expected arrival">
        <button type="submit">Apply</button>
      </form>`;
    return `<tr><td>${chamber}</td><td>${shortID(plan.id)}</td><td>${plan.direction}</td><td>${plan.vessels.length}</td><td>${priorityLabel(plan.priority)}</td><td>${formatETA(plan.eta)}</td><td>${new Date(plan.created_at).toLocaleTimeString()}</td><td>${status}</td><td>${controls}</td></tr>`;
  }));
  document.querySelector("#plans").innerHTML = rows.join("") || '<tr><td colspan="9" class="muted">No active plans</td></tr>';

  document.querySelectorAll(".schedule-form").forEach((scheduleForm) => {
    scheduleForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      const planID = scheduleForm.dataset.plan;
      const button = scheduleForm.querySelector("button");
      await withBusy(button, async () => {
        try {
          const localETA = scheduleForm.elements.eta.value;
          await api(`/api/plans/${planID}/schedule`, { method: "PATCH", body: JSON.stringify({
            priority: Number(scheduleForm.elements.priority.value),
            eta: localETA ? new Date(localETA).toISOString() : null,
          }) });
          setMessage(message, `Plan ${shortID(planID)} rescheduled; queue re-ordered.`);
          await refresh();
        } catch (error) { setMessage(message, error.message); }
      });
    });
  });

  const queueSections = Object.entries(queueByChamber).map(([chamber, queue]) => {
    if (!queue.length) return `<h3>${chamber}</h3><p class="muted small">Empty</p>`;
    const body = queue.map((entry, index) => `
      <tr><td>${index + 1}</td><td>${entry.vessel.name} <span class="muted small">(${shortID(entry.vessel.id)})</span></td><td>${shortID(entry.plan_id)}</td><td>${priorityLabel(entry.priority)}</td><td>${formatETA(entry.eta)}</td><td>${entry.arrived_at ? new Date(entry.arrived_at).toLocaleTimeString() : "-"}</td><td>${entry.released ? '<span class="badge badge-released">released</span>' : '<span class="badge badge-waiting">waiting</span>'}</td></tr>`).join("");
    return `<h3>${chamber}</h3><table><thead><tr><th>#</th><th>Vessel</th><th>Plan</th><th>Priority</th><th>ETA</th><th>Arrived</th><th>State</th></tr></thead><tbody>${body}</tbody></table>`;
  });
  document.querySelector("#queues").innerHTML = queueSections.join("");

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
      const localETA = document.querySelector("#eta").value;
      const result = await api("/api/plans", { method: "POST", body: JSON.stringify({
        chamber_id: document.querySelector("#chamber").value,
        direction: document.querySelector("#direction").value,
        priority: Number(document.querySelector("#priority").value),
        eta: localETA ? new Date(localETA).toISOString() : "",
        vessels: [{ name: document.querySelector("#vessel").value, call_sign: document.querySelector("#call-sign").value, length_m: Number(document.querySelector("#length").value) }],
      }) });
      setMessage(message, `Plan ${shortID(result.plan.id)} admitted to ${result.plan.chamber_id}.`);
      await refresh();
    } catch (error) { setMessage(message, error.message); }
  });
});

refresh().catch((error) => setMessage(message, error.message));
setInterval(() => refresh().catch(() => {}), 5000);
