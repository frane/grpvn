# grpvn v0.10.2

OAuth approval now continues without JavaScript. Authorization redirects to
a stable waiting page that refreshes every five seconds and offers a visible
Continue / check approval link. Reloading that page keeps the same request
and approval code instead of creating another request.

After approval the server redirects directly to the registered client
callback. Completed and expired requests show a clear final status. The page
retains PKCE, ticket validation, per-request approval and private no-store
headers. Existing identities, grants and OAuth credentials are preserved.

Regression tests exercise the entire waiting/approval/callback/token flow
without executing JavaScript and verify that refreshes create no new requests.
