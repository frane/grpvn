# grpvn 0.13.7

Conversation headers now show short channel and DM names in full. The title
button previously constrained itself against a parent whose width already
included its negative alignment margin, clipping seven pixels even when the
window had ample room. The shared web/desktop header now sizes the title using
flex layout and reserves space for its action buttons.

Long names truncate only when the available space requires it, with the full
name available on hover. Browser checks cover desktop, narrow windows, DMs and
cached remote workspaces.

Desktop downloads: macOS Apple Silicon, Windows x64 and Linux x64. macOS is
ad-hoc signed and not notarized; Windows is unsigned. Homebrew installs the
CLI; standalone desktop downloads are separate release assets.
