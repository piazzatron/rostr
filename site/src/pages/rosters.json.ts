import { PRESET_ROSTERS } from "../data/rosters";

/**
 * The pre-built rosters for the CLI, which fetches this at most once a day so new rosters reach it without a release.
 * Members are in draft order, each "<emoji> <name>" as in rosters.ts. A CLI ignores a version newer than it knows, so
 * bump it only for a change older CLIs would misread; adding fields is fine without one.
 */
export function GET() {
  const presets = PRESET_ROSTERS.map(({ id, emoji, name, source, members }) => ({ id, emoji, name, source, members }));
  return new Response(JSON.stringify({ version: 1, presets }), {
    headers: { "Content-Type": "application/json; charset=utf-8" },
  });
}
