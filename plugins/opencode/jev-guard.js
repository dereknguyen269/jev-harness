// OpenCode plugin — routes tool execution through the Jev guard service.
//
// Install: place in ~/.config/opencode/plugins/jev-guard.js
//          or .opencode/plugins/jev-guard.js
//
// The guard service must be running (see README for setup).
//
// Manual approval flow: when the guard answers "approval_required" it mints
// an approval (approval_id, ~30s TTL). Instead of failing, the plugin waits
// for a human to approve/deny it in the dashboard (or via the API) and then
// lets the tool proceed or blocks it.
//
// Env knobs:
//   JEV_GUARD_URL                  guard base URL (default http://127.0.0.1:8787)
//   JEV_GUARD_TIMEOUT_MS           per-request HTTP timeout (default 2000)
//   JEV_GUARD_APPROVAL             poll = wait for a human decision (default)
//                                  block = legacy behavior: throw immediately
//   JEV_GUARD_APPROVAL_POLL_MS     approval poll interval (default 1000)
//   JEV_GUARD_APPROVAL_TIMEOUT_MS  max wait (default expires_in*1000 + 2000)
//   JEV_AUTH_TOKEN                 bearer for approval polling — required only
//                                  when the server runs with --auth-token

const GUARD_URL = (process.env.JEV_GUARD_URL || "http://127.0.0.1:8787").replace(/\/+$/, "");
const GUARD_TIMEOUT = Number(process.env.JEV_GUARD_TIMEOUT_MS) || 2000;
const APPROVAL_MODE = (process.env.JEV_GUARD_APPROVAL || "poll").trim().toLowerCase();
const APPROVAL_POLL_MS = Number(process.env.JEV_GUARD_APPROVAL_POLL_MS) || 1000;
const AUTH_TOKEN = process.env.JEV_AUTH_TOKEN || "";

function guardHeaders() {
  const headers = { "Content-Type": "application/json" };
  if (AUTH_TOKEN) headers["Authorization"] = `Bearer ${AUTH_TOKEN}`;
  return headers;
}

export const JevGuard = async ({ project, directory, worktree, client, $ }) => {
  async function callGuard(tool, args) {
    const res = await fetch(`${GUARD_URL}/v1/check`, {
      method: "POST",
      headers: guardHeaders(),
      body: JSON.stringify({
        agent: { name: "opencode" },
        tool: { name: tool, args },
        context: { working_dir: directory || "", workspace: directory || "", platform: "opencode" },
      }),
      signal: AbortSignal.timeout(GUARD_TIMEOUT),
    });
    if (!res.ok) {
      // Fail-closed: treat HTTP errors as block
      throw new Error(`Jev guard returned HTTP ${res.status}`);
    }
    return await res.json();
  }

  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

  async function notify(level, message) {
    try {
      if (client && client.app && typeof client.app.log === "function") {
        await client.app.log({ body: { service: "jev-guard", level, message } });
      }
    } catch {
      // Logging must never break the gate.
    }
  }

  function approvalInstructions(decision) {
    const id = decision.approval_id;
    const reason = decision.reason || "risk detected";
    return [
      `Jev guard wants a human decision: ${reason} (risk ${decision.risk ?? "?"})`,
      `Approve: curl -X POST ${GUARD_URL}/v1/approvals/${id}/approve -H "Authorization: Bearer <token>"`,
      `Deny:    curl -X POST ${GUARD_URL}/v1/approvals/${id}/deny -H "Authorization: Bearer <token>"`,
      `Or open the dashboard: ${GUARD_URL}/`,
    ].join("\n");
  }

  // waitForApproval polls the approval until a human approves/denies it or
  // it expires. Resolves on approve; throws on deny/expire/timeout (block).
  async function waitForApproval(decision) {
    const ttlMs = (Number(decision.expires_in) || 30) * 1000;
    const timeoutMs = Number(process.env.JEV_GUARD_APPROVAL_TIMEOUT_MS) || ttlMs + 2000;
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      let approvals;
      try {
        const res = await fetch(`${GUARD_URL}/v1/approvals`, {
          headers: guardHeaders(),
          signal: AbortSignal.timeout(GUARD_TIMEOUT),
        });
        if (res.status === 401 || res.status === 403) {
          throw new Error(`cannot read approvals (HTTP ${res.status}) — set JEV_AUTH_TOKEN to a viewer-or-better token`);
        }
        if (!res.ok) {
          throw new Error(`approval poll returned HTTP ${res.status}`);
        }
        approvals = await res.json();
      } catch (err) {
        if (Date.now() >= deadline) throw err;
        await sleep(APPROVAL_POLL_MS);
        continue;
      }
      const item = Array.isArray(approvals) ? approvals.find((a) => a && a.id === decision.approval_id) : undefined;
      if (item) {
        if (item.status === "approved") return;
        if (item.status === "denied" || item.status === "expired") {
          throw new Error(`Jev guard approval ${decision.approval_id} was ${item.status}: ${decision.reason || "risk detected"}`);
        }
      }
      if (Date.now() >= deadline) {
        throw new Error(`Jev guard approval ${decision.approval_id} expired with no human decision — blocking to stay fail-closed`);
      }
      await sleep(APPROVAL_POLL_MS);
    }
  }

  return {
    "tool.execute.before": async (input, output) => {
      try {
        const decision = await callGuard(input.tool, output.args || {});
        if (decision.decision === "block") {
          throw new Error(`Blocked by Jev guard: ${decision.reason || "unknown reason"}`);
        }
        if (decision.decision === "approval_required" || decision.request_approval) {
          if (APPROVAL_MODE !== "poll" || !decision.approval_id) {
            throw new Error(`Approval required by Jev guard: ${decision.reason || "risk detected"}`);
          }
          const instructions = approvalInstructions(decision);
          await notify("warn", `approval_required for ${input.tool} — waiting on a human:\n${instructions}`);
          await waitForApproval(decision);
          await notify("info", `Jev guard approval ${decision.approval_id} approved — proceeding with ${input.tool}`);
          return;
        }
      } catch (err) {
        if (err.message && err.message.startsWith("Blocked by Jev guard")) {
          throw err;
        }
        if (err.message && err.message.startsWith("Approval required by Jev guard")) {
          throw err;
        }
        if (err.message && err.message.startsWith("Jev guard approval")) {
          throw err;
        }
        // Network/timeout errors → fail closed
        throw new Error(`Jev guard unavailable: ${err.message}`);
      }
    },
  };
};