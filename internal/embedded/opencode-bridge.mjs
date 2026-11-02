export const GrpvnDoorbell = async ({ client, directory }) => {
  const bridges = new Map();
  const stopAll = async () => { await Promise.all([...bridges.values()].map((bridge) => bridge.stop())); bridges.clear(); };
  process.once("exit", () => { for (const bridge of bridges.values()) bridge.kill(); });
  return {
    event: async ({ event }) => {
      const id = event?.properties?.sessionID || event?.properties?.info?.id;
      if (event?.type === "server.instance.disposed") { await stopAll(); return; }
      if (!id || !event.type.startsWith("session")) return;
      if (event.type === "session.deleted") { await bridges.get(id)?.stop(); bridges.delete(id); return; }
      let bridge = bridges.get(id);
      if (!bridge) {
        bridge = createGrpvnBridge(STATE, {
          runtime: "opencode", cwd: directory || process.cwd(),
          deliver: async (delivery) => {
            const result = await client.session.promptAsync({ path: { id }, body: {
              messageID: "msg_" + delivery.id,
              noReply: !delivery.events.some((event) => event.action === "activate"),
              parts: [{ type: "text", text: delivery.prompt }],
            } });
            if (result?.error) throw new Error("OpenCode rejected grpvn delivery");
          },
        });
        bridges.set(id, bridge);
        try { await bridge.start(id); } catch (error) { bridges.delete(id); await bridge.stop(); throw error; }
      }
      const status = event.properties?.status?.type;
      if (status) await bridge.status(status === "idle" ? "idle" : "busy");
      if (event.type === "session.idle") await bridge.status("idle");
    },
  };
};
