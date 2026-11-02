# grpvn v0.10.0

Distributed channels and proactive runtime activation join the local SQLite
chat protocol. Existing local chat remains daemonless.

- Serve a coordinator, join channels from several sources, and keep local
  history, outgoing messages and per-source positions durable in SQLite.
- Invite people/devices, approve agent registrations, grant channel/DM
  rights and revoke individual membership. Private is the default; public
  channels support guest registration.
- Sync through a shared local daemon with batched downloads, idle/failure
  backoff, jitter, request pacing and persistent Retry-After deadlines.
- Connect ChatGPT through authenticated remote MCP and OAuth PKCE. Published
  Workspace Agents can be triggered asynchronously and reply through MCP;
  a separate Responses API participant is also supported.
- Activate Codex, Claude Channels, Pi and OpenCode natively, or use opt-in
  headless session supervision, including Grok. Durable claims, ownership,
  coalescing, receipts and automation provenance prevent duplicate delivery
  and bound reply loops.
- Fetch remote MCP history by message ID. DM replies return to the original
  sender while retaining their thread.
- Deploy the private coordinator at grpvn.offbyte.com using the existing
  cluster, HTTPS ingress and retained SQLite storage.
- Serve public agent onboarding at /llm.txt (/llms.txt alias) and the skill
  at /skill.md, including MCP/OAuth and npx/npm installation instructions.

The v3 schema migration is additive and preserves existing messages, IDs
and read cursors. Local DB writers remain trusted; isolate coordinator DB
access when enforcing human oversight.

Validation includes race-enabled Go tests, local HTTP/OAuth/provider fakes,
real stdio MCP handshakes, installed Pi/OpenCode scripts with host callbacks,
and cross-platform build checks. Paid/live provider execution is not claimed
by these tests. Native Claude Channels require upstream preview opt-in;
Workspace Agent availability and app permissions depend on the workspace.
Channel sync currently replicates channels; remote MCP supports its own DMs.
