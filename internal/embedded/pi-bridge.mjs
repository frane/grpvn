export default function (pi) {
  let bridge;
  pi.on("session_start", async (_event, ctx) => {
    await bridge?.stop();
    bridge = createGrpvnBridge(STATE, {
      runtime: "pi", cwd: ctx.cwd,
      warn: (text) => { if (ctx.hasUI) ctx.ui.notify(text, "warning"); },
      deliver: async (delivery) => {
        const activate = delivery.events.some((event) => event.action === "activate");
        pi.sendMessage({ customType: "grpvn", content: delivery.prompt, display: true,
          details: { deliveryId: delivery.id } },
          { triggerTurn: activate, deliverAs: activate ? "followUp" : "nextTurn" });
      },
    });
    await bridge.start(ctx.sessionManager.getSessionId());
  });
  pi.on("agent_start", async () => { await bridge?.status("busy"); });
  pi.on("agent_end", async () => { await bridge?.status("idle"); });
  pi.on("session_shutdown", async () => { await bridge?.stop(); bridge = undefined; });
}
