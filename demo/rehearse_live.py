#!/usr/bin/env python3
"""Rehearsal check against the live deployment.

Answers the question you cannot answer by reading config: does a real MCP
tool call for `pip install reqeusts` actually come back BLOCKED on the
deployed server? Nothing exposes VIGIL_COMPROMISED_PACKAGES over HTTP —
firewall.NewCompromisedList() reads it once at construction — so the only
honest check is to make the call.

Runs the full OAuth 2.1 dance Claude runs (DCR -> authorize -> approve ->
PKCE token), initialises MCP, then makes three tool calls: one benign, then
the typosquat twice, so you can see the denylist catch strike one and memory
take over on strike two.

    python demo/rehearse_live.py                # client name "Claude Web"
    python demo/rehearse_live.py "Cursor"       # any other client name

The client name matters: VIGIL keys cross-session trust on it
(mcp/handler.go:300), so it is the agent id you will type into the Memory
Timeline. Run this before filming — it moves real trust on the deployment,
so reset afterwards with /archive.
"""
import json, ssl, sys, urllib.parse, urllib.request

BASE = "https://vigil-cuy2.onrender.com"
CTX = ssl.create_default_context()


def req(method, path, body=None, headers=None, form=False):
    url = BASE + path
    data, hdrs = None, {"Accept": "application/json"}
    if body is not None:
        if form:
            data = urllib.parse.urlencode(body).encode()
            hdrs["Content-Type"] = "application/x-www-form-urlencoded"
        else:
            data = json.dumps(body).encode()
            hdrs["Content-Type"] = "application/json"
    hdrs.update(headers or {})
    r = urllib.request.Request(url, data=data, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(r, context=CTX, timeout=90) as resp:
            raw = resp.read().decode()
            try:
                return json.loads(raw), resp.status
            except ValueError:
                return raw, resp.status
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try:
            return json.loads(raw), e.code
        except ValueError:
            return raw, e.code


CLIENT_NAME = sys.argv[1] if len(sys.argv) > 1 else "Claude Web"

d, s = req("POST", "/register",
           {"redirect_uris": ["https://claude.ai/api/mcp/auth_callback"],
            "client_name": CLIENT_NAME})
client_id = d.get("client_id", "")
print(f"  register        {s}  {client_id[:24]}...")

# /authorize redirects to the consent page; follow manually to get request id
opener = urllib.request.build_opener(type("NoRedirect", (urllib.request.HTTPRedirectHandler,),
                                          {"redirect_request": lambda *a, **k: None})())
auth = (f"{BASE}/authorize?client_id={urllib.parse.quote(client_id)}"
        "&redirect_uri=https%3A%2F%2Fclaude.ai%2Fapi%2Fmcp%2Fauth_callback"
        "&response_type=code&code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
        "&code_challenge_method=S256&state=x")
try:
    resp = opener.open(auth, timeout=90)
    loc = resp.getheader("Location", "")
except urllib.error.HTTPError as e:
    loc = e.headers.get("Location", "")
request_id = loc.split("request=")[1].split("&")[0] if "request=" in loc else ""
print(f"  authorize       -> /connect?request={request_id[:18]}...")

d, s = req("POST", "/api/v1/vigil/oauth/approve",
           {"request_id": request_id, "budget_limit": 5.0})
code = d.get("redirect_to", "").split("code=")[1].split("&")[0]
print(f"  approve         {s}  session={d.get('session_id','')[:20]}")

d, s = req("POST", "/token",
           {"grant_type": "authorization_code", "code": code,
            "code_verifier": "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk",
            "client_id": client_id,
            "redirect_uri": "https://claude.ai/api/mcp/auth_callback"}, form=True)
bearer = d.get("access_token", "")
print(f"  token           {s}  {bearer[:20]}...")

AUTH = {"Authorization": f"Bearer {bearer}"}
d, s = req("POST", "/api/v1/mcp/bearer",
           {"jsonrpc": "2.0", "id": 1, "method": "initialize",
            "params": {"protocolVersion": "2024-11-05", "capabilities": {},
                       "clientInfo": {"name": CLIENT_NAME, "version": "1.0"}}}, headers=AUTH)
print(f"  initialize      {s}  server={d.get('result',{}).get('serverInfo',{}).get('name','?')}")


def call(tool, args, label):
    d, s = req("POST", "/api/v1/mcp/bearer",
               {"jsonrpc": "2.0", "id": 2, "method": "tools/call",
                "params": {"name": tool, "arguments": args}}, headers=AUTH)
    res = d.get("result", {}) if isinstance(d, dict) else {}
    txt = json.dumps(res)[:300]
    # isError is the authoritative signal. Matching on words in the body
    # gave a false positive the first time this ran: a directory listing that
    # happened to contain "vigil-audit.jsonl" was reported as BLOCKED.
    blocked = bool(res.get("isError"))
    print(f"\n  [{label}] {tool} -> {'BLOCKED' if blocked else 'allowed'}")
    print(f"    {txt[:280]}")
    return blocked


call("list_directory", {"path": "."}, "benign")
b1 = call("run_command", {"command": "pip install reqeusts"}, "typosquat #1")
b2 = call("run_command", {"command": "pip install reqeusts"}, "typosquat #2")

print("\n" + "=" * 62)
print(f"  denylist catches strike 1 : {'YES' if b1 else 'NO — set VIGIL_COMPROMISED_PACKAGES'}")
print(f"  strike 2 also blocked     : {'YES' if b2 else 'NO'}")
print(f"  agent id for the demo     : {CLIENT_NAME!r}")
print("=" * 62)
