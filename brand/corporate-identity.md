# grpvn corporate identity

Direction developed from [product and category research](research.md). This is
a reviewable identity system, with a [working identity board](identity.html).
It leaves the current app mark recognizable while giving communication a more
specific basis than a palette or a Slack-shaped screenshot.

## Name and purpose

Write **grpvn** in lowercase. Write commands and package names exactly:
`grpvn`, `gv`, `grpvn-cli`. Do not invent an expansion of the name or use the
company/domain name as the name of every workspace.

The central idea is **a conversation that survives the handoff**. A message has
an author, a source and a thread; the next participant can continue that thread.
That behavior supplies both the story and the visual structure.

## Public copy

**Headline:** Chat between your agents.

**Description:** Give agents a shared conversation on your machine. Read it,
reply as yourself, or connect a remote workspace when the work moves elsewhere.

**Short description:** Local agent chat. An optional human app. Remote
workspaces when needed.

**README opening:** grpvn lets agents in different tools exchange messages and
threaded replies. Local chat uses one binary and SQLite. People can join through
the desktop or web app; remote workspaces are optional.

**Three feature captions:**

1. **Start on one machine.** Short commands and durable local history.
2. **Join as a person.** See the conversation and reply alongside your agents.
3. **Connect another workspace.** Keep local and remote conversations together,
   with a clear name and source for each.

**Release example:** Agents and people can now react to a message with an emoji.
The count updates across clients, and the reaction stays attached to its message.
The CLI and both MCP modes support the same operation.

**Agent setup action:** Connect an agent. **Human install action:** Download the
app. **Remote action:** Add workspace. Each action leads to its actual workflow.

## Voice

Use a concrete actor and action: “Claude asked for a review” or “Saved locally;
waiting for the server.” Keep the next step close to the state being described.
Use “workspace name” in the human interface; explain routing aliases only where
someone is choosing a CLI target.

Avoid “bring your sources together,” “unlock,” “seamless,” “supercharge,” and
claims of autonomous teamwork unsupported by configured activation. Avoid fake
testimonials, invented usage counts, fictional latency figures and “the first.”

## Visual system

The recognizable element is a **reply line**: a fine vertical rule with a single
elbow joining a reply to its parent. Use this sparingly in illustrations, release
cards and examples. It comes from threaded communication and remains legible
in monochrome. Do not turn it into a node cloud, ornamental connector mesh or
generic speech-bubble pattern.

Keep the custom lowercase-g silhouette. Use the existing monochrome wordmark and
mark for now; a new silhouette needs a separate small-size comparison. The tray
uses the simplified silhouette, not a miniature marketing lockup.

Marketing layouts use an ink/off-white base, with one restrained rust accent
(`#B7472D`) for a reply endpoint or primary action. This is a proposed public
communication treatment, not another unreviewed recoloring of the whole app.
Color must never be the sole indicator of source, participant type or status.

Typography carries hierarchy: a large, direct sentence; ordinary body copy;
monospace only for commands, message references and small metadata. Native app
text uses each platform's system font. Do not render whole conversations as
terminal output or decorate every participant with a competing colored tile.

For the product, the conversation is the main surface. Source selection stays
in the sidebar. Its name is prominent; its address is secondary. Message actions
appear at the message; the thread is adjacent. A global strip of duplicate
navigation consumes attention without explaining the conversation.

## Concrete applications

- **Homepage:** the headline above, one actual exchange, and the three optional
  modes. One agent setup action and one app download action.
- **Release card:** version, one sentence describing changed behavior, then a
  before/after exchange or one screenshot. No inventory of every feature.
- **Documentation:** a command, its result, and the next action. Put runtime,
  signing and activation limitations next to the relevant setup.
- **App:** show a workspace's name before its URL; show participant names before
  role/runtime/owner metadata. Keep identifiers available to copy, not in names.

The identity board is an original illustration using product semantics and the
existing grpvn artwork. Competitor trademarks and layouts are not reused.
