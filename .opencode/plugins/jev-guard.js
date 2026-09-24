// OpenCode plugin — routes tool execution through the Jev guard service.
//
// Install: place in ~/.config/opencode/plugins/jev-guard.js
//          or .opencode/plugins/jev-guard.js
//
// The guard service must be running (see README for setup).

const GUARD_URL = process.env.JEV_GUARD_URL || "http://127.0.0.1:8787";
const GUARD_TIMEOUT = Number(process.env.JEV_GUARD_TIMEOUT_MS) || 2000;

export const JevGuard = async ({ project, directory, worktree, client, $ }) => {
  async function callGuard(tool, args) {
    const v2body = JSON.stringify({
      agent: { name: "opencode" },
      tool: { name: tool, args },
      context: { working_dir: directory || "", platform: "opencode" },
    });
    // Prefer /v2/check; fall back to /v1/check only when the guard
    // predates /v2 (connection refused or 404). Any other non-OK from
    // /v2 is a real signal — fail closed instead of retrying legacy.
    try {
      const res = await fetch(`${GUARD_URL}/v2/check`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: v2body,
        signal: AbortSignal.timeout(GUARD_TIMEOUT),
      });
      if (res.ok) return await res.json();
      if (res.status !== 404) {
        throw new Error(`Jev guard returned HTTP ${res.status}`);
      }
    } catch (err) {
      if (err.message && err.message.startsWith("Jev guard returned HTTP")) {
        throw err;
      }
      // Network/timeout/404 → try legacy v1 below.
    }
    // Legacy v1 fallback (old guard binaries without /v2).
    const res = await fetch(`${GUARD_URL}/v1/check`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        tool,
        args,
        context: {
          user_request: "",
          working_dir: directory || "",
          platform: "opencode",
          agent: "opencode",
        },
      }),
      signal: AbortSignal.timeout(GUARD_TIMEOUT),
    });

    if (!res.ok) {
      // Fail-closed: treat HTTP errors as block
      throw new Error(`Jev guard returned HTTP ${res.status}`);
    }
    return await res.json();
  }

  return {
    "tool.execute.before": async (input, output) => {
      try {
        const decision = await callGuard(input.tool, output.args || {});
        if (decision.decision === "block") {
          throw new Error(`Blocked by Jev guard: ${decision.reason || "unknown reason"}`);
        }
        if (decision.decision === "approval_required" || decision.request_approval) {
          // OpenCode has no built-in approval hook — block and let user approve
          throw new Error(`Approval required by Jev guard: ${decision.reason || "risk detected"}`);
        }
      } catch (err) {
        if (err.message && err.message.startsWith("Blocked by Jev guard")) {
          throw err;
        }
        if (err.message && err.message.startsWith("Approval required by Jev guard")) {
          throw err;
        }
        // Network/timeout errors → fail closed
        throw new Error(`Jev guard unavailable: ${err.message}`);
      }
    },
  };
};