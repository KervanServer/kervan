const state = {
  token: "",
};

const byId = (id) => document.getElementById(id);

function setOut(id, value) {
  byId(id).textContent =
    typeof value === "string" ? value : JSON.stringify(value, null, 2);
}

async function req(path, opts = {}) {
  const headers = { ...(opts.headers || {}) };
  if (state.token) {
    headers.Authorization = `Bearer ${state.token}`;
  }
  const res = await fetch(path, { ...opts, headers });
  const text = await res.text();
  let data = text;
  try {
    data = JSON.parse(text);
  } catch {}
  if (!res.ok) {
    throw new Error(typeof data === "string" ? data : JSON.stringify(data));
  }
  return data;
}

async function refreshPanels() {
  try {
    const [health, users, transfers, audit] = await Promise.all([
      req("/health"),
      req("/api/users"),
      req("/api/transfers?limit=20"),
      req("/api/audit?limit=20"),
    ]);
    setOut("healthOut", health);
    setOut("usersOut", users);
    setOut("transfersOut", transfers);
    setOut("auditOut", audit);
  } catch (err) {
    setOut("healthOut", String(err));
  }
}

byId("loginForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const username = byId("username").value.trim();
  const password = byId("password").value;
  try {
    const data = await req("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });
    state.token = data.token;
    byId("loginStatus").textContent = `Authenticated as ${data.user.username}`;
    await refreshPanels();
  } catch (err) {
    byId("loginStatus").textContent = `Login failed: ${err.message}`;
  }
});

refreshPanels();

