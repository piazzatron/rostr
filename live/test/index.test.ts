import { runDurableObjectAlarm, runInDurableObject } from "cloudflare:test";
import { env, exports } from "cloudflare:workers";
import { describe, expect, it } from "vitest";

const BASE = "https://live.example";

const update = (body: unknown, secret = "test-secret") =>
  exports.default.fetch(`${BASE}/update`, {
    method: "POST",
    headers: { Authorization: `Bearer ${secret}` },
    body: JSON.stringify(body),
  });

const upgrade = (ip = "192.0.2.1") =>
  exports.default.fetch(`${BASE}/ws`, { headers: { Upgrade: "websocket", "CF-Connecting-IP": ip } });

// Opens a socket and returns it with a queue of the messages it receives
async function connect(ip?: string) {
  const response = await upgrade(ip);
  expect(response.status).toBe(101);
  const socket = response.webSocket!;
  const messages: unknown[] = [];
  let waiting: (() => void) | undefined;
  socket.addEventListener("message", (event: MessageEvent) => {
    messages.push(JSON.parse(event.data as string));
    waiting?.();
  });
  socket.accept();
  const next = async () => {
    if (messages.length === 0) await new Promise<void>((resolve) => (waiting = resolve));
    return messages.shift();
  };
  return { socket, next };
}

describe("POST /update", () => {
  it("rejects a missing or wrong secret", async () => {
    expect((await update({ working: 1, benched: 1 }, "wrong")).status).toBe(401);
    const response = await exports.default.fetch(`${BASE}/update`, {
      method: "POST",
      body: JSON.stringify({ working: 1, benched: 1 }),
    });
    expect(response.status).toBe(401);
  });

  it("rejects bad counts", async () => {
    for (const body of [null, {}, { working: 1 }, { working: -1, benched: 2 }, { working: 1.5, benched: 2 }, { working: "1", benched: 2 }]) {
      expect((await update(body)).status, JSON.stringify(body)).toBe(400);
    }
  });

  it("stores the counts for sockets that connect later", async () => {
    expect((await update({ working: 128, benched: 1402 })).status).toBe(200);
    expect(await (await connect()).next()).toEqual({ working: 128, benched: 1402 });
  });
});

describe("GET /ws", () => {
  it("sends the current counts, then every update", async () => {
    await update({ working: 5, benched: 7 });
    const a = await connect();
    const b = await connect();
    expect(await a.next()).toEqual({ working: 5, benched: 7 });
    expect(await b.next()).toEqual({ working: 5, benched: 7 });

    await update({ working: 6, benched: 6 });
    expect(await a.next()).toEqual({ working: 6, benched: 6 });
    expect(await b.next()).toEqual({ working: 6, benched: 6 });
  });

  it("closes cleanly when the browser sends anything", async () => {
    const { socket, next } = await connect();
    await next();
    const closed = new Promise<CloseEvent>((resolve) => socket.addEventListener("close", resolve));
    socket.send("hello");
    expect((await closed).code).toBe(1000);
  });

  it("finishes the close handshake when the browser leaves", async () => {
    const { socket, next } = await connect();
    await next();
    const closed = new Promise<CloseEvent>((resolve) => socket.addEventListener("close", resolve));
    socket.close(1001, "Tab closed");
    expect((await closed).code).toBe(1000);

    // The object still broadcasts to the sockets that remain
    const other = await connect();
    await other.next();
    await update({ working: 1, benched: 2 });
    expect(await other.next()).toEqual({ working: 1, benched: 2 });
  });

  it("caps sockets per IP", async () => {
    for (let i = 0; i < 10; i++) await connect("198.51.100.1");
    expect((await upgrade("198.51.100.1")).status).toBe(429);
    expect((await upgrade("198.51.100.2")).status).toBe(101);
  });

  it("closes sockets after an hour", async () => {
    const old = await connect("198.51.100.3");
    const fresh = await connect("198.51.100.4");
    await old.next();
    await fresh.next();
    const closed = new Promise<CloseEvent>((resolve) => old.socket.addEventListener("close", resolve));

    // Backdate one socket, then run the sweep alarm
    const stub = env.COUNTS.get(env.COUNTS.idFromName("global"));
    await runInDurableObject(stub, (_, state) => {
      for (const socket of state.getWebSockets("198.51.100.3")) socket.serializeAttachment({ openedAt: 0 });
    });
    expect(await runDurableObjectAlarm(stub)).toBe(true);
    expect((await closed).code).toBe(1000);

    // The fresh socket still gets updates
    await update({ working: 3, benched: 4 });
    expect(await fresh.next()).toEqual({ working: 3, benched: 4 });
  });

  it("needs an upgrade", async () => {
    expect((await exports.default.fetch(`${BASE}/ws`)).status).toBe(426);
  });
});
