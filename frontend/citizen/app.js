// Single configuration point for the deployed API Gateway base URL (…/Prod).
// Replace with your stack output base, e.g. https://xxxx.execute-api.us-east-1.amazonaws.com/Prod
const API_BASE_URL = "https://cqf62u1pt5.execute-api.us-east-1.amazonaws.com/Prod";

const form = document.getElementById("triage-form");
const formError = document.getElementById("form-error");
const resultSection = document.getElementById("result-section");
const formSection = document.getElementById("triage-form-section");
const submitBtn = document.getElementById("submit-btn");

const sessionIdEl = document.getElementById("session-id");
const urgencyEl = document.getElementById("urgency");
const adviceEl = document.getElementById("advice");
const emergencyBanner = document.getElementById("emergency-banner");

const facilityStatus = document.getElementById("facility-status");
const facilityDetails = document.getElementById("facility-details");

const contactBlock = document.getElementById("contact-doctor-block");
const contactCheck = document.getElementById("contact-doctor-check");
const contactBtn = document.getElementById("contact-doctor-btn");
const contactConfirm = document.getElementById("contact-confirm");
const contactError = document.getElementById("contact-error");

let currentSessionId = null;

function apiUrl(path) {
  const base = API_BASE_URL.replace(/\/$/, "");
  const clean = path.startsWith("/") ? path : `/${path}`;
  return `${base}${clean}`;
}

function showError(el, message) {
  el.hidden = false;
  el.textContent = message;
}

function hideError(el) {
  el.hidden = true;
  el.textContent = "";
}

function urgencyLabel(urgency) {
  switch (urgency) {
    case "self_care":
      return "Self care";
    case "visit_soon":
      return "Visit soon";
    case "emergency":
      return "Emergency";
    default:
      return urgency;
  }
}

async function parseJsonResponse(res) {
  let data = null;
  try {
    data = await res.json();
  } catch (_) {
    data = null;
  }
  return data;
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  hideError(formError);
  submitBtn.disabled = true;
  submitBtn.textContent = "Submitting…";

  const symptomsText = document.getElementById("symptoms").value.trim();
  const district = document.getElementById("district").value.trim();

  if (!symptomsText || !district) {
    showError(formError, "Please enter symptoms and select a district.");
    submitBtn.disabled = false;
    submitBtn.textContent = "Get guidance";
    return;
  }

  try {
    const res = await fetch(apiUrl("/triage"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ symptomsText, district }),
    });
    const data = await parseJsonResponse(res);
    if (!res.ok) {
      throw new Error((data && data.error) || `Triage failed (${res.status})`);
    }

    currentSessionId = data.sessionId;
    showResult(data);
    lookupFacility(district);
  } catch (err) {
    showError(formError, err.message || "Unable to reach triage service.");
  } finally {
    submitBtn.disabled = false;
    submitBtn.textContent = "Get guidance";
  }
});

function showResult(data) {
  formSection.hidden = true;
  resultSection.hidden = false;

  sessionIdEl.textContent = data.sessionId || "—";
  urgencyEl.textContent = urgencyLabel(data.urgency);
  urgencyEl.className = data.urgency === "emergency" ? "emergency" : "";
  adviceEl.textContent = data.adviceText || "";

  const isEmergency = data.urgency === "emergency";
  emergencyBanner.hidden = !isEmergency;

  contactConfirm.hidden = true;
  contactError.hidden = true;
  contactCheck.checked = false;
  contactBtn.disabled = true;

  if (!isEmergency && (data.urgency === "self_care" || data.urgency === "visit_soon")) {
    contactBlock.hidden = false;
  } else {
    contactBlock.hidden = true;
  }
}

async function lookupFacility(district) {
  facilityDetails.hidden = true;
  facilityStatus.hidden = false;
  facilityStatus.textContent = "Looking up open, doctor-staffed facilities…";

  try {
    const res = await fetch(
      apiUrl(`/facilities/available?district=${encodeURIComponent(district)}`),
      { method: "GET" }
    );
    const data = await parseJsonResponse(res);
    if (res.status === 404) {
      facilityStatus.textContent =
        "No currently open facility with a doctor reported available in this district.";
      return;
    }
    if (!res.ok) {
      throw new Error((data && data.error) || `Facility lookup failed (${res.status})`);
    }

    facilityStatus.hidden = true;
    facilityDetails.hidden = false;
    document.getElementById("facility-name").textContent = data.name || data.facilityId;
    document.getElementById("facility-district").textContent = data.district || "—";
    document.getElementById("facility-open").textContent = data.statusOpen ? "Yes" : "No";
    document.getElementById("facility-doctor").textContent = data.statusHasDoctor ? "Yes" : "No";
    document.getElementById("facility-medicine").textContent = data.statusHasMedicine ? "Yes" : "No";
    document.getElementById("facility-updated").textContent = data.lastUpdatedAt || "—";
  } catch (err) {
    facilityStatus.textContent = err.message || "Could not load facility information.";
  }
}

contactCheck.addEventListener("change", () => {
  contactBtn.disabled = !contactCheck.checked;
  hideError(contactError);
});

contactBtn.addEventListener("click", async () => {
  if (!currentSessionId || !contactCheck.checked) return;

  hideError(contactError);
  contactConfirm.hidden = true;
  contactBtn.disabled = true;
  contactBtn.textContent = "Sending request…";

  try {
    const res = await fetch(apiUrl("/triage/contact"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sessionId: currentSessionId }),
    });
    const data = await parseJsonResponse(res);
    if (!res.ok) {
      throw new Error((data && data.error) || `Contact request failed (${res.status})`);
    }

    contactConfirm.hidden = false;
    contactConfirm.textContent =
      "Your request for human follow-up / doctor contact was recorded. A care worker may reach out.";
    contactCheck.disabled = true;
  } catch (err) {
    showError(contactError, err.message || "Could not record contact request.");
    contactBtn.disabled = false;
  } finally {
    contactBtn.textContent = "Request doctor contact";
  }
});

document.getElementById("new-request-btn").addEventListener("click", () => {
  currentSessionId = null;
  resultSection.hidden = true;
  formSection.hidden = false;
  form.reset();
  contactCheck.disabled = false;
  hideError(formError);
});
