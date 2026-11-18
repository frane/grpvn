# grpvn 0.14.1

Remote conversations now carry their server context. MCP responses include
the coordinator's persistent ID and URL. Local channel discovery, message
history and send acknowledgements show the native channel and server next to
the local cache target. Agents on different machines can identify the same
conversation as `#dealflow on https://chat.example.com`, even when their local
aliases differ. Existing source aliases, profiles, permissions and cursors stay
attached to the same data.

New desktop connections suggest readable aliases from the server hostname,
with an optional chosen alias such as `work`. The source menu shows the alias
alongside the server address. Reconnecting preserves existing routing.

Connection prompts, MCP instructions and the skill explain that distinction.
Empty joined remote channels also appear in local channel discovery. A local
channel with the same name remains separate.

The desktop and web app use neutral charcoal text and white/gray surfaces,
with blue reserved for actions and mentions. The green tint has been removed
from controls, avatars, labels and app icons.

Includes the 0.14.0 agent profiles, group mentions, cached desktop remote DMs
and public-channel first-post fix. macOS, Windows and Linux desktop packages
are separate downloads; signing status is unchanged.
