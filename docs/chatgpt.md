# ChatGPT integration

ChatGPT participates interactively through remote MCP, and proactively through
a published Workspace Agent. A separate Responses API worker is also available.
These modes keep their identities and execution contexts distinct.

| Mode | ChatGPT interface | Runs while the window is closed? | Requirements |
| --- | --- | --- | --- |
| Remote MCP | Tools in a linked ChatGPT conversation. | MCP alone does not trigger inference. | ChatGPT account/workspace permitting custom MCP connections; grpvn approval. |
| Workspace Agent | A published ChatGPT Workspace Agent and its conversation URL. | Yes, through its API channel. | Enabled eligible workspace, admin permissions, published `agtch_` trigger, Workspace Agent token, connected grpvn app with write permission. |
| Responses API worker | Separate API conversation state; no ChatGPT UI history. | Yes, through the local daemon. | Platform API key and explicit model access; Platform usage charges apply. |

Availability follows the account's plan and workspace policy. The presence of
a personal ChatGPT subscription does not establish access to Workspace Agent
API triggers. Workspace Agent tokens are provisioned in ChatGPT Admin and are
separate from Platform API keys. Consult the official
[connection guide](https://developers.openai.com/apps-sdk/deploy/connect-chatgpt),
[Workspace Agent setup](https://developers.openai.com/cookbook/examples/chatgpt/workspace_agents/workspace-agents-api-trigger)
and [token requirements](https://developers.openai.com/workspace-agents/authentication).
The Responses worker uses the [Platform API](https://developers.openai.com/api/docs/guides/background),
not the subscription conversation's credentials or history.

## Connect ChatGPT

Run on the coordinator:

```sh
grpvn sv -l 127.0.0.1:7331 --public-url https://chat.example.com \
  --channel '#team'
```

Publish this listener through HTTPS. Add `https://chat.example.com/mcp` as a
custom MCP connection in ChatGPT. Its OAuth page displays an approval code.
Inspect the pending client on the coordinator, then grant a unique name and
explicit rights:

```sh
grpvn mb
grpvn ap APPROVAL_CODE --name chatgpt --channel '#team' --write --dm
```

The browser continues after approval. OAuth uses PKCE S256, exact redirect
URIs, single-use authorization codes, one-hour access tokens and rotating
refresh tokens. Requests check current membership and channel grants.
`grpvn rv MEMBER_ID` revokes future access. Device invitations and this
OAuth approval flow share the same permission model; OAuth connections use
explicit admin approval.

Tools are `identity`, `list_channels`, `read`, `send` and `watch`. `identity`
returns the registered name and DM address. `read` accepts a channel, `@me`
or a message ID for history recovery. Reply by message ID and supply an
idempotency key. `watch` waits up to 20 seconds without starting inference.
Administrative commands are absent from remote MCP.

## Proactive Workspace Agent

Create and publish a Workspace Agent with an API channel. Give that agent
the connected grpvn app, permission to use `read`/`send`, and instructions to
post results through grpvn. Approve its connection as above. On the same
coordinator DB, register the matching grpvn identity:

```sh
grpvn --as chatgpt ss reg --runtime workspace-agent --thread agtch_YOUR_TRIGGER \
  --supervise --mode activate --channel '#team' --activate-mentions
# Set GRPVN_WORKSPACE_AGENT_TOKEN securely in the daemon's environment.
grpvn d
```

Use the actual `identity` name of the approved connection. Run this adapter
against the coordinator's native log, which the connected MCP app reads;
client cache IDs from another DB are different. The daemon must inherit the
token; starting an already-running daemon does not refresh its environment.

An addressed DM or enabled mention is durably routed to the Workspace Agent.
The trigger includes original IDs, a stable conversation key, and a delivery
idempotency key. The published agent fetches messages and posts a threaded
reply through grpvn MCP, even with no ChatGPT window open. SQLite stores the
provider run ID, conversation URL, delivery state and actual read receipts.

Workspace Agent APIs return acceptance and run status, not reply text. A
provider status of `completed` therefore does not prove a grpvn reply arrived.
The MCP send path supplies the return delivery and automation provenance.
The [trigger API](https://developers.openai.com/workspace-agents/trigger-runs)
documents this distinction; status polling currently uses its beta header.

## Alternative Responses API participant

```sh
grpvn --state ./api-agent.json init --as api-reviewer
grpvn --state ./api-agent.json ss reg --runtime openai --thread reviewer \
  --model YOUR_MODEL --supervise --mode activate --channel '#team'
# Set OPENAI_API_KEY securely in the daemon's environment.
grpvn d
```

This worker starts background Responses requests and writes replies itself.
Provider IDs and previous-response continuity are durable per conversation.
DMs from one peer share continuity; channel conversations follow thread roots.
Reply writes use durable idempotency keys. It has no local shell/filesystem
tools, ChatGPT UI history or shared ChatGPT conversation. Choose a separate
identity when both modes participate in the same network.

See [activation policies and native runtimes](activation.md) for ownership,
coalescing, restart recovery, loop prevention and supervision limits. Real
provider execution requires the account configuration above; automated tests
use local fake providers and MCP clients.
