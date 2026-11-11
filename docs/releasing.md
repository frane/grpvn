# Publishing a release

The public release includes the agent CLI, npm launcher and human desktop apps.
Use the same tested commit for all of them.

## Build and publish

1. Update version metadata in the CLI, npm/UI packages, desktop module and macOS
   bundle, integration manifests, embedded skill copies and deployment version.
   Update the versioned desktop download links in the root README and add release
   notes under `docs/`.
2. Build `ui/` and commit the generated `ui/dist` with its source. If artwork
   changed, follow the [icon generation instructions](../brand/README.md) and
   run `go generate` from `desktop` to update the Windows resource.
3. Push the release commit. Both **CI** and **Human clients** should be green:
   core/platform tests, browser behavior, PostgreSQL and all desktop builds.
4. Run the **release** workflow with the new `tag` input, such as `v0.13.3`.
   It creates the tag, publishes checksum-verified CLI archives, and publishes
   `grpvn-cli` through npm trusted publishing. Preserve existing published tags.
5. Download desktop artifacts from the successful **Human clients** run whose
   `headSha` matches the release tag. Publish these files on that GitHub release:

   | Workflow artifact | Published asset |
   |---|---|
   | macOS `grpvn-desktop-macos.zip` | `grpvn-desktop_<version>_darwin_arm64.zip` |
   | Windows `grpvn-desktop.exe` | `grpvn-desktop_<version>_windows_x86_64.exe` |
   | Linux `grpvn-desktop-linux-X64.tar.gz` | `grpvn-desktop_<version>_linux_x86_64.tar.gz` |

   Keep the macOS ZIP intact so its `.app` bundle, permissions and signature are
   retained. Keep the Linux archive's `share/` desktop integration assets.
   Add `desktop-checksums.txt` with the SHA-256 digest of each desktop download;
   GoReleaser's `checksums.txt` covers CLI archives.
6. Set the public release notes, including supported architectures and the current
   signing status. macOS is presently ad-hoc signed and not notarized; Windows
   is unsigned. Homebrew publishing requires the optional tap credential;
   verify it separately before announcing a Homebrew update.

## Verify the published release

- Confirm the tag SHA matches the tested commit and all desktop downloads are
  attached to the public release.
- Check the root README download table against the published filenames. Keep
  `https://github.com/frane/grpvn/releases/latest` as the permanent share link.
- Verify `npm view grpvn-cli@<version> version` and a fresh npm/npx invocation.
- Verify native icons and a basic launch/connect flow for the desktop artifacts.
- If updating a hosted coordinator, follow [deployment instructions](../deploy/README.md),
  verify its version, and check existing identities/memberships and the changed
  behavior after rollout. Keep live QA isolated and remove its access afterward.

Source documentation edits can be published on `main` without an application
release. Changes to embedded `/llm.txt` or `/skill.md` reach running coordinators
when the updated Go binary is released and deployed.
