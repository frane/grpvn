import { execFile, spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { promisify } from "node:util";

// Policy and delivery positions live in SQLite. This client only transports
// claimed events to the host runtime and records its acceptance separately.
function createGrpvnBridge(state, { runtime, deliver, cwd = process.cwd(), warn = () => {} }) {
  const binary = process.env.GRPVN_BIN || "grpvn";
  const args = ["--state", state, "--scope", "project"];
  const env = { ...process.env, GRPVN_STATE: state, GRPVN_SCOPE: "project" };
  // Chat commands issued by the native agent use this same project identity.
  process.env.GRPVN_STATE = state;
  process.env.GRPVN_SCOPE = "project";
  const execute = promisify(execFile);
  const run = async (...rest) => (await execute(binary, [...args, ...rest], { cwd, env, timeout: 10000, maxBuffer: 1024 * 1024 })).stdout.trim();
  let child, timer, registry, active = false, generation = 0, delay = 500;
  let deliveries = Promise.resolve();
  const report = (error) => warn("grpvn bridge: " + String(error?.message || error).slice(0, 300));
  const arm = (version) => {
    if (!active || version !== generation) return;
    child = spawn(binary, [...args, "wa", "--session", registry], { cwd, env, stdio: ["ignore", "pipe", "pipe"] });
    const lines = createInterface({ input: child.stdout });
    lines.on("line", (line) => {
      deliveries = deliveries.then(async () => {
        if (!active || version !== generation) return;
        let event;
        try {
          event = JSON.parse(line);
          await deliver(event);
          await run("ss", "ack", event.id);
          delay = 500;
        } catch (error) {
          if (event?.id) await run("ss", "ack", event.id, "--status", "uncertain").catch(report);
          report(error);
        }
      });
    });
    child.stderr.on("data", () => {}); // don't mix worker diagnostics with model output
    child.on("error", report);
    child.on("close", () => {
      lines.close();
      if (!active || version !== generation) return;
      timer = setTimeout(() => arm(version), delay + Math.random() * delay * 0.2);
      timer.unref?.();
      delay = Math.min(delay * 2, 30000);
    });
  };
  const stop = async () => {
    active = false; generation++;
    clearTimeout(timer);
    const previous = child; child = undefined;
    if (previous && previous.exitCode === null) {
      await new Promise((resolve) => {
        previous.once("close", resolve);
        previous.kill();
        const deadline = setTimeout(resolve, 1500); deadline.unref?.();
      });
    }
    await deliveries;
    if (registry) await run("ss", "status", registry, "offline").catch(report);
  };
  return {
    async start(thread) {
      await stop();
      registry = await run("ss", "reg", "--runtime", runtime, "--thread", thread,
        "--mode", process.env.GRPVN_ACTIVATION_MODE || "notify", "--keep-policy");
      active = true; arm(generation);
      return registry;
    },
    async status(status) { if (registry) await run("ss", "status", registry, status).catch(report); },
    stop,
    kill() { active = false; generation++; clearTimeout(timer); child?.kill(); },
  };
}
