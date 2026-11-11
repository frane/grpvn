# grpvn 0.13.3

Email invitations now open with readable spaces and proper line breaks in Mail
and other email applications. The mailto URI uses percent encoding rather than
form encoding, which had displayed literal plus signs in subjects and bodies.
Invitation links and existing profiles are preserved.

A custom grpvn logo now appears throughout the web app, sign-in screen,
favicon and project README. Desktop builds include the same artwork as a
macOS app/Dock icon, embedded Windows executable icon, Linux launcher icon,
and native system-tray icon. Editable SVG artwork is in `brand/`.

Regression coverage checks the raw invitation URI as email applications read
it, plus Windows' ability to load the embedded application icon.
