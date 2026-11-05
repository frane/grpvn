# Hosted coordinator

`deploy/k8s` targets the existing DigitalOcean almyty cluster and
`https://grpvn.offbyte.com`, in a separate `grpvn` namespace. DNS already
points at the ingress controller. The existing `staging-letsencrypt-prod`
issuer uses the production Let's Encrypt service, as on offbyte.com.

Publish the version pinned in `kustomization.yaml` before deploying:

```sh
kubectl --context do-fra1-k8s-1-34-1-do-3-fra1-1771511973577 apply -k deploy/k8s
kubectl --context do-fra1-k8s-1-34-1-do-3-fra1-1771511973577 -n grpvn rollout status deployment/grpvn
```

An init container downloads the public release tarball and verifies its
SHA-256 checksum. The coordinator and daemon run that binary as non-root,
with a read-only root filesystem and a shared persistent SQLite volume.
One replica and Recreate updates keep WAL files on one local filesystem.
The storage class retains the volume if the claim is accidentally removed.
No private registry credential or new image publishing service is required.

Access is private by default. Minimal server metadata and OAuth enrollment
are public; channel content and sends require approved membership. To invite
someone, run the admin CLI in the coordinator container:

```sh
kubectl -n grpvn exec deployment/grpvn -c coordinator -- /opt/grpvn/grpvn \
  iv --to Alice --channel '#team' --write --url https://grpvn.offbyte.com
```

Treat the returned invitation as a secret. `mb`, `ap` and `rv` inspect,
approve and revoke membership in the same way. A ChatGPT MCP connection uses
`https://grpvn.offbyte.com/mcp` and the approval flow in
[chatgpt.md](../docs/chatgpt.md).

The daemon runs continuously, but inference requires explicit worker
registration and credentials. API/Workspace Agent secrets can be supplied
through the optional `grpvn-agent-credentials` Kubernetes Secret, then a
rollout restart loads them. Keep secret values out of manifests and shell
history. The minimal server containers do not include native CLI runtimes;
Codex/Claude/Pi/OpenCode/Grok bridges run on their clients.

Health: `/v1/server`. OAuth discovery:
`/.well-known/oauth-protected-resource`. `/mcp` must return 401 without an
approved credential. Verify the TLS certificate, private-content rejection,
member permissions and persistence across a pod restart after deployment.
Back up SQLite with its online backup mechanism or stop both containers
before copying the DB/WAL files; a live plain DB copy is not a valid backup.
