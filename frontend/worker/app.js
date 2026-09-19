// Single configuration point for the deployed API Gateway base URL (…/Prod).
const API_BASE_URL = "https://cqf62u1pt5.execute-api.us-east-1.amazonaws.com/Prod";

const SESSION_KEY = "rhn_demo_worker_session";

const loginSection = document.getElementById("login-section");
const dashboard = document.getElementById("dashboard");
const loginForm = document.getElementById("login-form");
const loginError = document.getElementById("login-error");
const workerNameEl = document.getElementById("worker-name");
const workerDistrictEl = document.getElementById("worker-district");
const workerFacilityEl = document.getElementById("worker-facility");
const queueStatus = document.getElementById("queue-status");
const queueList = document.getElementById("queue-list");
const pendingCountEl = document.getElementById("pending-count");
const ackedCountEl = document.getElementById("acked-count");
const hideAckedEl = document.getElementById("hide-acked");
const facilityForm = document.getElementById("facility-form");
const facilityMsg = document.getElementById("facility-msg");
const facilityError = document.getElementById("facility-error");

let cachedSessions = [];

function apiUrl(path) {
  const base = API_BASE_URL.replace(/\/$/, "");
  const clean = path.startsWith("/") ? path : `/${path}`;
  return `${base}${clean}`;
}

function showError(el, message) {
  el.hidden = false;
  el.textContent = message;
}

function hideMsg(el) {
  el.hidden = true;
  el.textContent = "";
}

function getSession() {
  try {
    const raw = sessionStorage.getItem(SESSION_KEY);
    return raw ? JSON.parse(raw) : null;
  } catch (_) {
    return null;
  }
}

function setSession(data) {
  sessionStorage.setItem(
    SESSION_KEY,
    JSON.stringify({
      workerId: data.workerId,
      username: data.username,
      district: data.district,
      facilityId: data.facilityId,
      facilityName: data.facilityName,
      demo: true,
      loggedInAt: new Date().toISOString(),
    })
  );
}

function clearSession() {
  sessionStorage.removeItem(SESSION_KEY);
}

function requireLogin() {
  const session = getSession();
  if (!session || !session.workerId) {
    loginSection.hidden = false;
    dashboard.hidden = true;
    return null;
  }
  loginSection.hidden = true;
  dashboard.hidden = false;
  workerNameEl.textContent = session.username;
  workerDistrictEl.textContent = session.district;
  workerFacilityEl.textContent = session.facilityName || session.facilityId;
  return session;
}

async function parseJsonResponse(res) {
  try {
    return await res.json();
  } catch (_) {
    return null;
  }
}

loginForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  hideMsg(loginError);

  const username = document.getElementById("username").value.trim();
  const password = document.getElementById("password").value;

  try {
    const res = await fetch(apiUrl("/worker/login"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });
    const data = await parseJsonResponse(res);
    if (!res.ok) {
      throw new Error((data && data.error) || `Login failed (${res.status})`);
    }
    setSession(data);
    requireLogin();
    await loadQueue();
  } catch (err) {
    showError(loginError, err.message || "Demo login failed.");
  }
});

document.getElementById("logout-btn").addEventListener("click", () => {
  clearSession();
  cachedSessions = [];
  queueList.innerHTML = "";
  requireLogin();
});

document.getElementById("refresh-btn").addEventListener("click", () => {
  loadQueue();
});

hideAckedEl.addEventListener("change", () => {
  renderQueue(cachedSessions);
});

function urgencyLabel(urgency) {
  switch (urgency) {
    case "self_care":
      return "Self care";
    case "visit_soon":
      return "Visit soon";
    case "emergency":
      return "Emergency";
    default:
      return urgency || "—";
  }
}

function escapeHtml(value) {
  return String(value ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

async function loadQueue() {
  const session = requireLogin();
  if (!session) return;

  queueStatus.hidden = false;
  queueStatus.textContent = "Loading queue…";
  queueList.innerHTML = "";

  try {
    const res = await fetch(
      apiUrl(`/worker/queue?workerId=${encodeURIComponent(session.workerId)}`),
      { method: "GET" }
    );
    const data = await parseJsonResponse(res);
    if (!res.ok) {
      throw new Error((data && data.error) || `Queue load failed (${res.status})`);
    }
    cachedSessions = Array.isArray(data) ? data : [];
    renderQueue(cachedSessions);
  } catch (err) {
    queueStatus.textContent = err.message || "Could not load worker queue.";
  }
}

function renderQueue(sessions) {
  const pending = sessions.filter((s) => s.status !== "acknowledged");
  const acked = sessions.filter((s) => s.status === "acknowledged");
  pendingCountEl.textContent = String(pending.length);
  ackedCountEl.textContent = String(acked.length);

  const hideAcked = hideAckedEl.checked;
  const visible = hideAcked ? pending : sessions;

  if (visible.length === 0) {
    queueStatus.hidden = false;
    queueStatus.textContent = hideAcked
      ? "No pending cases for your facility."
      : "No triage sessions for your facility.";
    queueList.innerHTML = "";
    return;
  }

  queueStatus.hidden = true;
  const pendingBlock = pending.length
    ? `<h3 class="section-label">Pending</h3>${pending.map(renderQueueItem).join("")}`
    : "";
  const ackedVisible = hideAcked ? [] : acked;
  const ackedBlock = ackedVisible.length
    ? `<h3 class="section-label">Acknowledged</h3>${ackedVisible.map(renderQueueItem).join("")}`
    : "";
  queueList.innerHTML = pendingBlock + ackedBlock;

  queueList.querySelectorAll(".ack-btn").forEach((btn) => {
    btn.addEventListener("click", () => acknowledgeSession(btn.dataset.sessionId, btn));
  });
}

function renderQueueItem(session) {
  const contact = !!session.contactDoctorRequested;
  const acked = session.status === "acknowledged";
  const classes = [
    "queue-item",
    session.urgency === "emergency" ? "emergency" : "",
    contact ? "contact-requested" : "",
  ]
    .filter(Boolean)
    .join(" ");

  const badges = [
    `<span class="badge urgency-${escapeHtml(session.urgency)}">${escapeHtml(urgencyLabel(session.urgency))}</span>`,
    contact ? `<span class="badge contact">Doctor contact requested</span>` : "",
    acked ? `<span class="badge acked">Acknowledged</span>` : `<span class="badge">Pending</span>`,
  ].join("");

  const ackButton = acked
    ? ""
    : `<button type="button" class="ack-btn" data-session-id="${escapeHtml(session.sessionId)}">Acknowledge</button>`;

  const contactDetails = session.contactDetails || session.contactNumber || "";
  const contactRow = contactDetails
    ? `<dt>Contact details</dt><dd>${escapeHtml(contactDetails)}</dd>`
    : `<dt>Contact details</dt><dd>Not provided</dd>`;

  const ackMeta = acked
    ? `<dt>Acknowledged by</dt><dd>${escapeHtml(session.acknowledgedBy || "—")}</dd>
       <dt>Acknowledged at</dt><dd>${escapeHtml(session.acknowledgedAt || "—")}</dd>`
    : "";

  return `
    <article class="${classes}">
      <h3>Session ${escapeHtml(session.sessionId)}</h3>
      <div class="badge-row">${badges}</div>
      <dl>
        <dt>District</dt><dd>${escapeHtml(session.district)}</dd>
        <dt>Facility</dt><dd>${escapeHtml(session.facilityName || session.facilityId || "—")}</dd>
        <dt>Symptoms</dt><dd>${escapeHtml(session.symptomsText)}</dd>
        <dt>Advice</dt><dd>${escapeHtml(session.adviceText)}</dd>
        ${contactRow}
        <dt>Created</dt><dd>${escapeHtml(session.createdAt)}</dd>
        <dt>Status</dt><dd>${escapeHtml(session.status || "pending")}</dd>
        ${ackMeta}
      </dl>
      ${ackButton}
    </article>
  `;
}

async function acknowledgeSession(sessionId, button) {
  const session = requireLogin();
  if (!session || !sessionId) return;

  button.disabled = true;
  button.textContent = "Acknowledging…";

  try {
    const res = await fetch(apiUrl("/worker/ack"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sessionId, workerId: session.workerId }),
    });
    const data = await parseJsonResponse(res);
    if (!res.ok) {
      throw new Error((data && data.error) || `Acknowledge failed (${res.status})`);
    }
    await loadQueue();
  } catch (err) {
    button.disabled = false;
    button.textContent = "Acknowledge";
    alert(err.message || "Could not acknowledge session.");
  }
}

facilityForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const session = requireLogin();
  if (!session) return;

  hideMsg(facilityMsg);
  hideMsg(facilityError);

  const statusOpen = facilityForm.elements.statusOpen.value === "true";
  const statusHasDoctor = facilityForm.elements.statusHasDoctor.value === "true";
  const statusHasMedicine = facilityForm.elements.statusHasMedicine.value === "true";

  try {
    const res = await fetch(apiUrl("/worker/status"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        workerId: session.workerId,
        statusOpen,
        statusHasDoctor,
        statusHasMedicine,
      }),
    });
    const data = await parseJsonResponse(res);
    if (!res.ok) {
      throw new Error((data && data.error) || `Status update failed (${res.status})`);
    }
    facilityMsg.hidden = false;
    facilityMsg.textContent = `Status updated for ${data.facilityName || data.facilityId} at ${data.lastUpdatedAt}.`;
  } catch (err) {
    showError(facilityError, err.message || "Could not update facility status.");
  }
});

if (requireLogin()) {
  loadQueue();
}
