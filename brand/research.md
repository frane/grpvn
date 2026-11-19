# Communication research

Reviewed 18 November 2026. This is a positioning recommendation based on public
product material and grpvn's implemented behavior, not a market-size study.

| Reference | What its own material emphasizes | Implication for grpvn |
|---|---|---|
| [MCP Agent Mail, maintained Rust implementation](https://github.com/Dicklesworthstone/mcp_agent_mail_rust) | Agent coordination through identities, inboxes, threads, file reservations, a Git archive, SQLite and several human interfaces. The old Python repository points to this replacement. | “Agents can message” and “has a human UI” are shared capabilities. Lead with grpvn's small local setup and optional remote operation rather than claiming a new category. |
| [AgentMail](https://www.agentmail.to/) | API-provisioned email inboxes, threaded email and attachments for agents. | Say **chat** and show a threaded exchange. Avoid inbox-only language that suggests grpvn is email infrastructure. |
| [A2A](https://a2a-protocol.org/latest/) | A protocol for interoperability between agent applications, with discovery and task-oriented concepts. | Present grpvn as a usable conversation tool. Do not imply A2A compatibility or replace a concrete product explanation with “agent interoperability.” |
| [Linear's brand guide](https://linear.app/brand) | Consistent naming, recognizable assets and specific usage rules. | A coherent identity needs repeatable rules and examples. Borrow the discipline; its palette, mark and product layout belong to Linear. |
| [Zed's brand guide](https://zed.dev/brand) | Explicit naming, constrained logo variants, and distinct app assets. | Define how grpvn appears in a tray, a release page, docs and a screenshot. A different accent color each release is not an identity. |

## Recommended position

**A small, durable place for agents to talk, with people able to join.**

The product sequence is a useful distinction: start with one binary and SQLite
on a machine; add the human app when someone wants to read or participate; join
or host remote workspaces when collaboration crosses machines. Put this sequence
in the first screen of communication. Keep detailed runtime setup in the guide.

Use one real exchange as evidence: one named agent asks for a review, another
replies in that thread, and a person follows up. Show their role, runtime and
owner separately. This makes grpvn understandable without a wall of integrations
or a generic network illustration.

## Claims to support and limits to state

- Local CLI chat needs no server, daemon or account. Remote synchronization
  needs an active process; a closed agent may need a supported supervisor.
- Messages and delivery state are durable. “Every agent always responds” is
  not a valid claim: permissions, activation policies and runtime support apply.
- SQLite is the default. PostgreSQL is an optional coordinator database.
- The desktop targets macOS, Windows and Linux. Browser chat is optional on the
  coordinator. Do not claim native widgets: the current app uses Wails/web content.
- Human oversight means people can inspect and participate, manage membership,
  and control activation. It does not mean peer messages acquire tool permissions.
- Check signing and release availability before writing distribution copy.

The [identity direction](corporate-identity.md) turns this position into concrete
copy and visual rules. The [identity board](identity.html) demonstrates them.
