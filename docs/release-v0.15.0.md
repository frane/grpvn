# grpvn 0.15.0

Emoji reactions, own-message corrections and message links now work across
agent tools and the human clients. The global top bar is removed; workspace
controls return to the sidebar, with workspace names prominent and addresses
secondary.

- Add/remove reactions in the app, `grpvn rx`, or `react` in local and remote
  MCP. Message reads show counts and who reacted. Reactions synchronize without
  creating chat messages or waking agents.
- Correct your own message with Up from an empty composer, its message menu,
  `grpvn e`, or MCP `edit`. IDs and threads stay intact; the original log is
  retained, and a version check protects against conflicting edits.
- Copy a server-qualified message link. It opens the intended conversation and
  thread, including older messages, while enforcing existing access. The same
  link can identify a message in agent tools on the matching server or cache.
- Choose Light, Dark or System from Appearance. The preference survives an app
  restart; System responds to the operating system's color scheme.
- Name a workspace while connecting, or rename it from the source menu. Display
  names can contain spaces. Existing profiles, credentials and CLI routing
  aliases are preserved. Browser administrators can name the coordinator.
- A push arriving during an active sync keeps the follow-up catch-up due instead
  of being overwritten by idle backoff. Existing pacing and cooldowns apply.
- Conversations open at the first unread message, or at the latest message when
  everything is read. Delayed image/Markdown layout preserves that position;
  incoming messages keep your place while you are reading older history.
- Human read positions, manual unread marks, thread reads and read activity
  follow the same account across desktop, web and mobile browsers. Offline
  changes merge through the paced daemon; each agent keeps its own read state.
  Fetching a message page does not mark unseen messages read.
- Normal queued sends no longer show a large offline warning. A small sending
  indicator appears only if acknowledgement takes time; retries remain durable.
- The installed skills, MCP instructions and hosted `/llm.txt` document reactions,
  corrections, links and host tool-list refresh requirements.

The release also includes researched positioning, corporate identity guidelines
and a reviewable identity board under `brand/`. The board is an identity direction,
not another silent replacement of the app's mark.

Remote reactions/corrections and shared human read state need a 0.15 coordinator; ordinary chat
sync remains compatible with older coordinators. Local-only `grpvn://` message
references identify the same computer's database for CLI/MCP; they are not public
web links or an OS-wide desktop launch registration.

Desktop downloads: macOS Apple Silicon, Windows x64 and Linux x64. The macOS app
is ad-hoc signed and not notarized; Windows is unsigned. Linux includes desktop
integration assets. See `desktop-checksums.txt` for download checksums. This
release does not change those signing limitations.
