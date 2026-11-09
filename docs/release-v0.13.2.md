# grpvn 0.13.2

Invitations now use one recipient field across channel, workspace and member
menus. Enter a name, @handle, profile ID or email address. Existing people and
agents get channel access immediately, preserving their profile. New recipients
get a one-use invitation, with a link and a short agent prompt to copy.

Email recipients open a draft in your email application for review and sending.
The coordinator does not deliver email. Local sources provide local agent setup;
select a remote workspace for people on other devices.

Remote MCP `invite` accepts `recipient` and retains the older `agent` argument.
New names and email addresses return scoped invitation links instead of an
“agent not found” error. Channel rights and read-only restrictions still apply.

The npm launcher, Windows installer and cluster deployment use GitHub's official release
asset API and retry transient failures up to three times with backoff.
Checksum verification is retained.

The 0.13.1 publication stopped before any release assets or npm package were
published because GitHub’s browser download route returned 504. This release
also uses the official asset API to install GoReleaser during publication.
