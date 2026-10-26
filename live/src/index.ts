// The rostr live Worker: one Durable Object holds the home page's counts and pushes each update to every open socket.
// See docs/live-counts.md.
import { DurableObject } from "cloudflare:workers";

export interface Env {
  COUNTS: DurableObjectNamespace<LiveCounts>;
  LIVE_SECRET: string;
}

export interface Counts {
  working: number;
  benched: number;
}

// Every visitor shares the one object, so sockets are capped in total and per IP, and each is closed after an hour
// (the page reconnects). The alarm that closes them runs every 10 minutes while any are open.
const MAX_SOCKETS = 10_000;
const MAX_SOCKETS_PER_IP = 10;
const MAX_SOCKET_AGE_MS = 60 * 60 * 1000;
const SWEEP_EVERY_MS = 10 * 60 * 1000;

export default {
  async fetch(request, env): Promise<Response> {
    const live = env.COUNTS.get(env.COUNTS.idFromName("global"));
    const { pathname } = new URL(request.url);

    switch (`${request.method} ${pathname}`) {
      case "GET /ws":
        if (request.headers.get("Upgrade") !== "websocket") {
          return new Response("Expected a WebSocket upgrade.", { status: 426 });
        }
        return live.fetch(request);

      case "POST /update": {
        if (!authorized(request, env.LIVE_SECRET)) {
          return new Response("Missing or invalid secret.", { status: 401 });
        }
        const counts = parseCounts(await request.json().catch(() => null));
        if (!counts) {
          return new Response('Expected {"working": n, "benched": m} with whole numbers 0 or more.', { status: 400 });
        }
        await live.update(counts);
        return Response.json(counts);
      }
    }
    return new Response("Not found.", { status: 404 });
  },
} satisfies ExportedHandler<Env>;

/**
 * The single, global counts object. Sockets use the hibernation API, so the object is evicted from memory between
 * updates while the sockets stay open, and idle tabs cost nothing.
 */
export class LiveCounts extends DurableObject<Env> {
  // WebSocket upgrades only; the Worker routes everything else to the RPC methods below
  async fetch(request: Request): Promise<Response> {
    // Each socket is tagged with its IP, so the object can count an IP's sockets
    const ip = request.headers.get("CF-Connecting-IP") ?? "unknown";
    if (this.ctx.getWebSockets().length >= MAX_SOCKETS || this.ctx.getWebSockets(ip).length >= MAX_SOCKETS_PER_IP) {
      return new Response("Too many connections.", { status: 429 });
    }

    const [client, server] = Object.values(new WebSocketPair());
    this.ctx.acceptWebSocket(server, [ip]);
    server.serializeAttachment({ openedAt: Date.now() } satisfies SocketInfo);
    if ((await this.ctx.storage.getAlarm()) === null) {
      await this.ctx.storage.setAlarm(Date.now() + SWEEP_EVERY_MS);
    }
    server.send(JSON.stringify(await this.counts()));
    return new Response(null, { status: 101, webSocket: client });
  }

  // Close sockets past their lifetime, and come back while any are open
  async alarm(): Promise<void> {
    const sockets = this.ctx.getWebSockets();
    for (const socket of sockets) {
      const { openedAt } = socket.deserializeAttachment() as SocketInfo;
      if (Date.now() - openedAt < MAX_SOCKET_AGE_MS) continue;
      try {
        socket.close(1000, "Reconnect.");
      } catch {}
    }
    if (sockets.length > 0) {
      await this.ctx.storage.setAlarm(Date.now() + SWEEP_EVERY_MS);
    }
  }

  async counts(): Promise<Counts> {
    return (await this.ctx.storage.get<Counts>("counts")) ?? { working: 0, benched: 0 };
  }

  async update(counts: Counts): Promise<void> {
    await this.ctx.storage.put("counts", counts);
    const message = JSON.stringify(counts);
    for (const socket of this.ctx.getWebSockets()) {
      // A socket that's closing throws; the rest still get the update
      try {
        socket.send(message);
      } catch {}
    }
  }

  // Browsers only listen, so anything they send ends the connection
  webSocketMessage(socket: WebSocket): void {
    socket.close(1000, "This socket only sends.");
  }

  // Finish the close handshake a browser starts when its tab goes away
  webSocketClose(socket: WebSocket): void {
    socket.close(1000, "Bye.");
  }
}

interface SocketInfo {
  openedAt: number;
}

function authorized(request: Request, secret: string | undefined): boolean {
  // Without a secret configured, nobody is authorized
  if (!secret) return false;
  const encoder = new TextEncoder();
  const given = encoder.encode(request.headers.get("Authorization") ?? "");
  const expected = encoder.encode(`Bearer ${secret}`);
  return given.byteLength === expected.byteLength && crypto.subtle.timingSafeEqual(given, expected);
}

function parseCounts(body: unknown): Counts | null {
  const { working, benched } = (body ?? {}) as Record<string, unknown>;
  const isCount = (n: unknown): n is number => Number.isInteger(n) && (n as number) >= 0;
  return isCount(working) && isCount(benched) ? { working, benched } : null;
}
