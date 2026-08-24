/**
 * VIGIL — Agent Commerce Protocol transport bridge.
 *
 * This is deliberately thin. It does exactly two things:
 *
 *   1. registers VIGIL as an ACP provider on Base (chainId 8453), and
 *   2. forwards each inbound job to the Go decision engine at
 *      POST /vigil/acp/job, then returns that verdict to the network.
 *
 * It contains NO decision logic. Counterparty trust is decided in
 * pkg/acp/service.go from the same local memory the firewall uses — one
 * SQLite lookup, ~1.5ms, no LLM. Reimplementing that here in JavaScript
 * would give two sources of truth that could disagree about whether an
 * agent is banned, which is exactly the bug a memory layer exists to
 * prevent.
 *
 * Why a bridge at all: the ACP SDK is Node-only, and VIGIL is Go. The
 * alternative — reimplementing ACP's wire protocol and registration in Go —
 * would mean maintaining a second implementation of someone else's protocol.
 *
 *   npm install
 *   npm run register          # register on Base, then exit
 *   npm start                 # register and serve jobs
 *
 * Environment:
 *   VIGIL_ACP_PRIVATE_KEY     signer for on-chain registration (required)
 *   VIGIL_ACP_ENTITY_ID       Virtuals entity id
 *   VIGIL_ACP_WALLET_ADDRESS  agent wallet address
 *   VIGIL_API_URL             Go engine (default http://127.0.0.1:8080)
 *   VIGIL_ACP_CHAIN_ID        8453 mainnet (default), 84532 Sepolia
 *
 * With no private key this refuses to claim registration it does not have,
 * prints why, and stays in forward-only mode. It never reports Registered
 * when it is not.
 */

import http from "node:http";

const API = process.env.VIGIL_API_URL || "http://127.0.0.1:8080";
const CHAIN_ID = Number(process.env.VIGIL_ACP_CHAIN_ID || 8453);
const PRIVATE_KEY = process.env.VIGIL_ACP_PRIVATE_KEY || "";
const ENTITY_ID = process.env.VIGIL_ACP_ENTITY_ID || "";
const WALLET = process.env.VIGIL_ACP_WALLET_ADDRESS || "";
const REGISTER_ONLY = process.argv.includes("--register-only");

/**
 * Ask the Go engine to decide one job.
 *
 * The verdict is whatever pkg/acp/service.go returns — ALLOW / BLOCK /
 * REVIEW, with the recalled trust score and the reason. A 503 means the
 * memory layer could not answer, which must surface as REVIEW rather than
 * being silently upgraded to ALLOW: an unknown counterparty is unproven,
 * not trusted.
 */
async function decide(job) {
  const res = await fetch(`${API}/api/v1/vigil/acp/job`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      job_id: job.id ?? job.job_id,
      buyer_agent_id: job.buyerAgentId ?? job.buyer_agent_id ?? job.clientAddress,
      requested_tool: job.requestedTool ?? job.requested_tool ?? job.serviceName,
      intent: job.intent ?? job.serviceRequirement ?? "",
    }),
  });

  const verdict = await res.json();
  if (!res.ok) {
    // 503 = trust unavailable. Do not convert that into approval.
    console.error(
      `[vigil-acp] engine returned ${res.status} for job ${verdict.job_id}: ${verdict.reason}`,
    );
  }
  console.log(
    `[vigil-acp] job=${verdict.job_id} buyer=${verdict.buyer_agent_id} ` +
      `verdict=${verdict.verdict} trust=${verdict.trust_score} ` +
      `recall=${verdict.recall_ms}ms source=${verdict.source}`,
  );
  return verdict;
}

async function main() {
  if (!PRIVATE_KEY) {
    console.error(
      "[vigil-acp] VIGIL_ACP_PRIVATE_KEY is not set — cannot register on chain " +
        `${CHAIN_ID}. Running in forward-only mode: jobs POSTed to this process ` +
        "are still decided by the Go engine, but VIGIL is NOT registered as an " +
        "ACP provider and this bridge will not claim otherwise.",
    );
    if (REGISTER_ONLY) process.exit(1);
    return serveForwardOnly();
  }

  // Imported lazily so forward-only mode works without the dependency
  // installed, and so a missing SDK is reported plainly rather than as an
  // unrelated startup crash.
  let AcpClient, AcpContractClient;
  try {
    ({ default: AcpClient, AcpContractClient } = await import(
      "@virtuals-protocol/acp-node"
    ));
  } catch (err) {
    console.error(
      "[vigil-acp] @virtuals-protocol/acp-node is not installed. " +
        "Run `npm install` in services/acp-node.",
    );
    process.exit(1);
  }

  const contract = await AcpContractClient.build(PRIVATE_KEY, ENTITY_ID, WALLET, {
    chainId: CHAIN_ID,
  });

  const client = new AcpClient({
    acpContractClient: contract,
    // Every inbound job is forwarded verbatim. The bridge does not filter,
    // score, or pre-judge — that would put decision logic in two places.
    onNewTask: async (job) => {
      try {
        const verdict = await decide(job);
        if (verdict.verdict === "ALLOW") {
          await job.accept?.(`VIGIL: trust ${verdict.trust_score}`);
        } else {
          await job.reject?.(verdict.reason);
        }
      } catch (err) {
        // Never accept on error. A transport failure is not a trust signal.
        console.error(`[vigil-acp] job handling failed: ${err.message}`);
        await job.reject?.("VIGIL: counterparty trust could not be evaluated");
      }
    },
  });

  await client.init();
  console.log(
    `[vigil-acp] registered on chain ${CHAIN_ID} as ${WALLET} (entity ${ENTITY_ID}); ` +
      `decisions delegated to ${API}/api/v1/vigil/acp/job`,
  );

  if (REGISTER_ONLY) {
    console.log("[vigil-acp] --register-only: registration complete, exiting.");
    process.exit(0);
  }
}

/**
 * Forward-only mode: a local HTTP endpoint that proxies jobs to the engine.
 * Useful for exercising the full path without an on-chain identity, and for
 * the demo.
 */
function serveForwardOnly() {
  const port = Number(process.env.VIGIL_ACP_BRIDGE_PORT || 8899);
  http
    .createServer(async (req, res) => {
      if (req.method !== "POST") {
        res.writeHead(405).end();
        return;
      }
      let body = "";
      for await (const chunk of req) body += chunk;
      try {
        const verdict = await decide(JSON.parse(body));
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(JSON.stringify(verdict));
      } catch (err) {
        res.writeHead(502, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ error: err.message }));
      }
    })
    .listen(port, () =>
      console.log(`[vigil-acp] forward-only bridge on :${port} -> ${API}`),
    );
}

main().catch((err) => {
  console.error("[vigil-acp] fatal:", err);
  process.exit(1);
});
