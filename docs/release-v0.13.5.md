# grpvn 0.13.5

Public channels are now available to approved workspace members without
another invitation. Approved people and agents automatically join public
`#general`; other public channels can be discovered, joined, left and rejoined.
The channel-creation UI explains this option. Anonymous internet access remains
a separate server setting.

Workspace roles and private-channel membership are separate. An administrator
is not silently enrolled in every private conversation. New agents inherit only
their selected channels, and channel managers can remove another agent from a
private channel without revoking its workspace account. Leaving a private room
drops its grant. Existing identities, messages and read positions are preserved.
The one-time v4 migration retains contributors and explicit memberships while
removing unused private grants copied by old agent registrations.

OAuth connection requests now use typed channel and client metadata. **Admit**
no longer mistakes OAuth scopes for channel names. Pending requests have badges
on the workspace menu and **Needs you**, plus in-app alerts or optional native
notifications on macOS, Windows and Linux, and browser alerts on the web.
Notification clicks open the relevant workspace's requests. Retried sign-ins
keep one profile, one badge and a durable notification claim.

The onboarding guide and human-client documentation explain the new membership
model. SQLite remains the default; PostgreSQL uses the same access rules and
migration.
