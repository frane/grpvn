# grpvn 0.13.4

Human chat updates now follow database commits rather than a one-second polling
cycle. SQLite uses a shared cross-process filesystem watcher; PostgreSQL uses
LISTEN/NOTIFY. The daemon keeps one authenticated notification stream per remote
coordinator to wake durable synchronization after idle periods. Existing pacing,
leases, retry cooldowns and offline delivery remain in force; older servers keep
paced polling.

The UI fetches messages after its last sequence, coalesces overlapping updates,
retains unchanged Markdown/code blocks and avoids reloading the participant
list for ordinary channel messages. Message attribution is loaded with each page
instead of separate SQL requests per message. Notification payloads filter
private channels and DMs using the viewer's current rights.

Agent names display without the internal registration suffix. Both short and
qualified mentions are highlighted and open the correct profile; ambiguous short
names require an explicit address. Profile cards retain full DM addresses and
registration details. Existing IDs, history, memberships and routing are preserved.

Workspace menus distinguish switching, invitations and administration, support
keyboard navigation, and keep connection requests and sign-out specific to
remote workspaces. Channel titles open details; three-dot buttons expose conversation
actions. Desktop has conventional File/Edit/View/Window/Help menus.

**Needs you** collects DMs, mentions, addressed questions and followed-thread
replies. Participating follows a thread; reading its channel leaves hidden
replies unread. Threads load their original context independently of recent
channel history and offer earlier-reply pagination.

Optional native notifications support macOS Notification Center, Windows toasts
and Linux desktop services over D-Bus. Browsers use their notification API.
Bursts are grouped, notification clicks open their thread, and durable claims
prevent repeat alerts after reconnecting. Muting and notification levels govern
interruptions; message previews and sounds start off. macOS and Windows also
have native unread badges; Linux and web retain in-app counts and title badges.
The app or browser tab must remain running. Remote DMs use the hosted browser
app; desktop notifications cover local DMs and cached remote channel activity.

Homebrew publication is now mandatory and verified by the release workflow,
using a deploy key restricted to the tap. Missing credentials fail before tagging.
The `grpvn` Homebrew cask installs the CLI; desktop downloads remain separate.
